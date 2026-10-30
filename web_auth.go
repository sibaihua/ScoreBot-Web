package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	sessionCookieName = "sb_session"
	stateCookieName   = "sb_oauth_state"
	sessionTTL        = 30 * 24 * time.Hour
	stateTTL          = 10 * time.Minute
)

type oauthConfig struct {
	ClientID     string
	ClientSecret string
	AuthURL      string
	TokenURL     string
	UserinfoURL  string
	RedirectURI  string
	Scope        string
}

var (
	oauthCfg      *oauthConfig
	sessionSecret []byte
)

// loadOAuthConfig 读取 OAuth 配置。客户端 ID 或密钥缺失时返回 nil，表示不启用登录。
func loadOAuthConfig() *oauthConfig {
	cfg := &oauthConfig{
		ClientID:     strings.TrimSpace(os.Getenv("OAUTH_CLIENT_ID")),
		ClientSecret: strings.TrimSpace(os.Getenv("OAUTH_CLIENT_SECRET")),
		AuthURL:      strings.TrimSpace(defaultString(os.Getenv("OAUTH_AUTH_URL"), "https://pan.sibh.cn/session/authorize")),
		TokenURL:     strings.TrimSpace(defaultString(os.Getenv("OAUTH_TOKEN_URL"), "https://pan.sibh.cn/api/v4/session/oauth/token")),
		UserinfoURL:  strings.TrimSpace(defaultString(os.Getenv("OAUTH_USERINFO_URL"), "https://pan.sibh.cn/api/v4/session/oauth/userinfo")),
		RedirectURI:  strings.TrimSpace(defaultString(os.Getenv("OAUTH_REDIRECT_URI"), "https://grade.sibh.cn/")),
		Scope:        strings.TrimSpace(defaultString(os.Getenv("OAUTH_SCOPE"), "openid email profile")),
	}
	if cfg.ClientID == "" || cfg.ClientSecret == "" {
		return nil
	}
	return cfg
}

// initSessionSecret 准备会话签名密钥：优先环境变量，其次密钥文件，最后自动生成并落盘。
func initSessionSecret() {
	if s := strings.TrimSpace(os.Getenv("SESSION_SECRET")); s != "" {
		sessionSecret = []byte(s)
		return
	}
	path := strings.TrimSpace(defaultString(os.Getenv("SESSION_KEY_FILE"), "/opt/scorebot/session.key"))
	if b, err := os.ReadFile(path); err == nil && len(strings.TrimSpace(string(b))) > 0 {
		sessionSecret = []byte(strings.TrimSpace(string(b)))
		return
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		log.Printf("> 生成会话密钥失败，回退到不安全的固定值: %v", err)
		sessionSecret = []byte("scorebot-insecure-fallback-secret")
		return
	}
	secret := hex.EncodeToString(buf)
	if err := os.WriteFile(path, []byte(secret), 0600); err != nil {
		log.Printf("> 会话密钥写入 %s 失败（仅在内存中生效）: %v", path, err)
	}
	sessionSecret = []byte(secret)
}

func signSession(payload string) string {
	mac := hmac.New(sha256.New, sessionSecret)
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func encodeSession(uid string) string {
	raw, _ := json.Marshal(map[string]any{"uid": uid, "exp": time.Now().Add(sessionTTL).Unix()})
	payload := base64.RawURLEncoding.EncodeToString(raw)
	return payload + "." + signSession(payload)
}

func decodeSession(token string) (string, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", false
	}
	if !hmac.Equal([]byte(signSession(parts[0])), []byte(parts[1])) {
		return "", false
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", false
	}
	var m struct {
		UID string `json:"uid"`
		Exp int64  `json:"exp"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return "", false
	}
	if m.UID == "" || m.Exp < time.Now().Unix() {
		return "", false
	}
	return m.UID, true
}

// requestLooksSecure 站点经 CDN 回源时，Go 侧看到的是 http，需按转发头判断。
func requestLooksSecure(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")), "https")
}

func setAuthCookie(w http.ResponseWriter, r *http.Request, name, value string, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		MaxAge:   int(ttl.Seconds()),
		HttpOnly: true,
		Secure:   requestLooksSecure(r),
		SameSite: http.SameSiteLaxMode,
	})
}

func clearAuthCookie(w http.ResponseWriter, r *http.Request, name string) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   requestLooksSecure(r),
		SameSite: http.SameSiteLaxMode,
	})
}

func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}

// sessionUserID 从请求中取出已登录用户的本地 ID。
func sessionUserID(r *http.Request) string {
	c, err := r.Cookie(sessionCookieName)
	if err != nil {
		return ""
	}
	uid, ok := decodeSession(c.Value)
	if !ok {
		return ""
	}
	return uid
}

func handleOAuthLogin(w http.ResponseWriter, r *http.Request) {
	if oauthCfg == nil {
		writeJSON(w, http.StatusNotImplemented, map[string]any{"ok": false, "msg": "未启用 OAuth 登录"})
		return
	}
	state := randomHex(16)
	setAuthCookie(w, r, stateCookieName, state, stateTTL)

	u, err := url.Parse(oauthCfg.AuthURL)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "msg": "授权端点配置有误"})
		return
	}
	q := u.Query()
	q.Set("client_id", oauthCfg.ClientID)
	q.Set("redirect_uri", oauthCfg.RedirectURI)
	q.Set("response_type", "code")
	q.Set("scope", oauthCfg.Scope)
	q.Set("state", state)
	u.RawQuery = q.Encode()

	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, u.String(), http.StatusFound)
}

type oauthCallbackRequest struct {
	Code  string `json:"code"`
	State string `json:"state"`
}

func handleOAuthCallback(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if oauthCfg == nil {
		writeJSON(w, http.StatusNotImplemented, map[string]any{"ok": false, "msg": "未启用 OAuth 登录"})
		return
	}
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "msg": "仅支持 POST 请求"})
		return
	}

	// code 既可来自 JSON 请求体，也可来自表单（兼容不同接入方式）。
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	state := strings.TrimSpace(r.URL.Query().Get("state"))
	if code == "" {
		var req oauthCallbackRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err == nil {
			code = strings.TrimSpace(req.Code)
			state = strings.TrimSpace(req.State)
		}
	}
	if code == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "msg": "缺少授权码"})
		return
	}

	// 校验 state，防 CSRF。
	stateCookie, err := r.Cookie(stateCookieName)
	if err != nil || stateCookie.Value == "" || stateCookie.Value != state {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "msg": "state 校验失败，请重新登录"})
		return
	}

	token, errMsg := exchangeOAuthCode(code)
	if errMsg != "" {
		writeJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "msg": errMsg})
		return
	}

	info, errMsg := fetchOAuthUserinfo(token)
	if errMsg != "" {
		writeJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "msg": errMsg})
		return
	}

	subject, email, nickname := extractOAuthIdentity(info)
	if subject == "" {
		writeJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "msg": "未能从 OAuth 服务获取用户标识"})
		return
	}

	userID := "oauth_" + sanitizeUserKey(subject)
	// 登录后自动创建本地用户记录，并保存资料（查分账号在绑定时写入）。
	profile := opView(userID)
	merged := map[string]any{}
	for k, v := range profile {
		merged[k] = v
	}
	merged["oauth_subject"] = subject
	merged["oauth_email"] = email
	merged["oauth_nickname"] = nickname
	merged["oauth_login_at"] = time.Now().Format(time.RFC3339)
	opWrite(userID, merged)

	clearAuthCookie(w, r, stateCookieName)
	setAuthCookie(w, r, sessionCookieName, encodeSession(userID), sessionTTL)

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"user_id":  userID,
		"email":    email,
		"nickname": nickname,
		"bound":    asString(opView(userID)["mode"]) != "",
	})
}

func exchangeOAuthCode(code string) (string, string) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("client_id", oauthCfg.ClientID)
	form.Set("client_secret", oauthCfg.ClientSecret)
	form.Set("redirect_uri", oauthCfg.RedirectURI)

	resp, err := http.PostForm(oauthCfg.TokenURL, form)
	if err != nil {
		return "", "无法连接 OAuth 令牌端点: " + err.Error()
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return "", fmt.Sprintf("OAuth 令牌端点返回 %d: %s", resp.StatusCode, truncateText(string(raw), 200))
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return "", "令牌响应解析失败"
	}
	if errDesc := asString(m["error"]); errDesc != "" {
		return "", "OAuth 返回错误: " + defaultString(asString(m["error_description"]), errDesc)
	}
	token := asString(m["access_token"])
	if token == "" {
		return "", "令牌响应中没有 access_token"
	}
	return token, ""
}

func fetchOAuthUserinfo(token string) (map[string]any, string) {
	req, err := http.NewRequest(http.MethodGet, oauthCfg.UserinfoURL, nil)
	if err != nil {
		return nil, "构造用户信息请求失败"
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, "无法连接 OAuth 用户信息端点: " + err.Error()
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return nil, fmt.Sprintf("用户信息端点返回 %d: %s", resp.StatusCode, truncateText(string(raw), 200))
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, "用户信息响应解析失败"
	}
	// 兼容 Cloudreve 风格的 {"code":0,"data":{...}} 包装。
	if inner := asMap(m["data"]); len(inner) > 0 {
		merged := map[string]any{}
		for k, v := range m {
			merged[k] = v
		}
		for k, v := range inner {
			merged[k] = v
		}
		return merged, ""
	}
	return m, ""
}

// extractOAuthIdentity 从不同 OAuth 实现的字段命名中取出用户标识。
func extractOAuthIdentity(info map[string]any) (subject, email, nickname string) {
	email = firstNonEmpty(info, "email", "mail", "user_email")
	nickname = firstNonEmpty(info, "nickname", "nick_name", "name", "preferred_username", "user_name", "username")
	for _, key := range []string{"sub", "openid", "id", "user_id", "uid", "userID"} {
		if v := asString(info[key]); v != "" {
			return v, email, nickname
		}
	}
	if email != "" {
		return email, email, nickname
	}
	return "", email, nickname
}

func firstNonEmpty(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v := asString(m[k]); v != "" {
			return v
		}
	}
	return ""
}

// sanitizeUserKey 把 OAuth 用户标识收敛成安全的存储键。
func sanitizeUserKey(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	out := b.String()
	if len(out) > 64 {
		out = out[:64]
	}
	return out
}

func handleAuthMe(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	uid := sessionUserID(r)
	if uid == "" {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":          true,
			"logged_in":   false,
			"oauth_ready": oauthCfg != nil,
		})
		return
	}
	profile := opView(uid)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"logged_in": true,
		"user_id":   uid,
		"email":     asString(profile["oauth_email"]),
		"nickname":  asString(profile["oauth_nickname"]),
		"bound":     asString(profile["mode"]) != "",
		"mode":      asString(profile["mode"]),
		"account":   asString(profile["zh"]),
		"name":      asString(profile["name"]),
		"school":    asString(profile["school"]),
	})
}

func handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	clearAuthCookie(w, r, sessionCookieName)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func truncateText(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}
