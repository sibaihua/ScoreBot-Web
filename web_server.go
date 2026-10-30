package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

// encodeWebImageDataURL 把图片字节编码成可直接放进 <img src> 的 data URL。
func encodeWebImageDataURL(content []byte) string {
	mime := http.DetectContentType(content)
	if mime == "application/octet-stream" {
		mime = "image/png"
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(content)
}

type webCommandRequest struct {
	UserID   string `json:"user_id"`
	UserName string `json:"user_name"`
	Content  string `json:"content"`
	Token    string `json:"token"`
}

type webCommandResponse struct {
	OK     bool       `json:"ok"`
	Texts  []string   `json:"texts"`
	Images []WebImage `json:"images"`
}

// runWebServer 启动网页模式：静态资源 + /api/command 命令接口。
func runWebServer(ctx context.Context) error {
	addr := defaultString(os.Getenv("WEB_ADDR"), ":8787")
	staticDir := defaultString(os.Getenv("WEB_DIR"), "web")
	requiredToken := strings.TrimSpace(os.Getenv("WEB_ACCESS_TOKEN"))

	initSessionSecret()
	oauthCfg = loadOAuthConfig()
	if oauthCfg != nil {
		log.Printf("web adapter OAuth enabled (redirect_uri: %s)", oauthCfg.RedirectURI)
	}

	mux := http.NewServeMux()
	// 同时注册两套前缀：/api/ 为标准入口，/sbapi/ 用于绕过 CDN 上已缓存的旧 /api/ 响应。
	for _, prefix := range []string{"/api/", "/sbapi/"} {
		p := prefix
		mux.HandleFunc(p+"health", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "adapter": "web"})
		})
		mux.HandleFunc(p+"command", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			handleWebCommand(w, r, requiredToken)
		})
		mux.HandleFunc(p+"auth/login", handleOAuthLogin)
		mux.HandleFunc(p+"auth/callback", handleOAuthCallback)
		mux.HandleFunc(p+"auth/me", handleAuthMe)
		mux.HandleFunc(p+"auth/logout", handleAuthLogout)
		mux.HandleFunc(p+"profile", handleWebProfile)
		mux.HandleFunc(p+"exams", handleWebExams)
		mux.HandleFunc(p+"exam/detail", handleWebExamDetail)
		mux.HandleFunc(p+"bind", handleWebBind)
		mux.HandleFunc(p+"unbind", handleWebUnbind)
		mux.HandleFunc(p+"unclaimed", handleWebUnclaimed)
		mux.HandleFunc(p+"claim", handleWebClaim)
		mux.HandleFunc(p+"switch", handleWebSwitch)
	}
	if fi, err := os.Stat(staticDir); err == nil && fi.IsDir() {
		fs := http.FileServer(http.Dir(staticDir))
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			// 首页是登录入口，不能被 CDN 缓存，否则登录态判断会失真。
			if isIndexRequest(r) {
				w.Header().Set("Cache-Control", "no-store")
			}
			fs.ServeHTTP(w, r)
		})
	} else {
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			fmt.Fprintf(w, "ScoreBot-Go web adapter is running. Static dir %q not found.\n", staticDir)
		})
	}

	server := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 20 * time.Second,
		WriteTimeout:      180 * time.Second,
	}
	log.Printf("web adapter listening on %s (static dir: %s)", addr, staticDir)
	errCh := make(chan error, 1)
	go func() { errCh <- server.ListenAndServe() }()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	}
}

func isIndexRequest(r *http.Request) bool {
	p := r.URL.Path
	return p == "/" || p == "" || strings.HasSuffix(p, "/index.html")
}

// handleWebCommand 保留原命令通道，供调试与高级用法使用。
func handleWebCommand(w http.ResponseWriter, r *http.Request, requiredToken string) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, webCommandResponse{
			OK:    false,
			Texts: []string{"* 错误: 仅支持 POST 请求。"},
		})
		return
	}

	var req webCommandRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, webCommandResponse{
			OK:    false,
			Texts: []string{"* 错误: 请求体解析失败。"},
		})
		return
	}
	if requiredToken != "" && req.Token != requiredToken {
		writeJSON(w, http.StatusUnauthorized, webCommandResponse{
			OK:    false,
			Texts: []string{"* 错误: 访问口令不正确。"},
		})
		return
	}

	// 已登录用户的身份一律以服务端会话为准，忽略请求体里的 user_id，避免越权读取他人数据。
	loggedIn := sessionUserID(r)
	if loggedIn == "" && oauthCfg != nil {
		writeJSON(w, http.StatusUnauthorized, webCommandResponse{
			OK:    false,
			Texts: []string{"* 错误: 请先登录后再操作。"},
		})
		return
	}

	userID := strings.TrimSpace(req.UserID)
	if loggedIn != "" {
		userID = loggedIn
	}
	if userID == "" {
		userID = defaultString(os.Getenv("CLI_USER_ID"), "web-user")
	}
	userName := strings.TrimSpace(req.UserName)
	if loggedIn != "" {
		if nick := asString(opView(loggedIn)["oauth_nickname"]); nick != "" {
			userName = nick
		} else if userName == "" {
			userName = loggedIn
		}
	}
	if userName == "" {
		userName = "Web User"
	}

	seq := time.Now().UnixNano()
	msgCtx := &MessageContext{
		Ctx:       r.Context(),
		Body:      map[string]any{},
		Platform:  "web",
		Event:     "WEB_COMMAND",
		ID:        fmt.Sprintf("web-%d", seq),
		Timestamp: time.Now().Format(time.RFC3339),
		UserID:    userID,
		UserName:  userName,
		GuildID:   "web",
		ChannelID: "web",
		Seq:       fmt.Sprintf("%d", seq),
		Content:   normalizeContent(req.Content),
	}

	sender := NewWebChatSender()
	handler := newCommandHandler(sender)
	handler.handle(msgCtx)

	resp := webCommandResponse{
		OK:     true,
		Texts:  sender.Texts,
		Images: sender.Images,
	}
	if len(resp.Texts) == 0 {
		resp.Texts = []string{}
	}
	if len(resp.Images) == 0 {
		resp.Images = []WebImage{}
	}
	writeJSON(w, http.StatusOK, resp)
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(payload); err != nil {
		http.Error(w, "encode response failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}
