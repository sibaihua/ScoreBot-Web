package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ---------- 结构化接口：让前端像普通网页一样取数据，而不是模拟机器人对话 ----------

type webExamItem struct {
	GUID      string  `json:"guid"`
	Name      string  `json:"name"`
	Time      string  `json:"time"`
	Type      string  `json:"type"`
	Score     string  `json:"score"`
	ScoreNum  float64 `json:"score_num"`
	ShortID   string  `json:"short_id"`
	Published bool    `json:"published"`
}

type webBindRequest struct {
	Platform string `json:"platform"`
	Account  string `json:"account"`
	Password string `json:"password"`
}

// platformAliases 把前端传来的平台标识收敛成 /绑定账号 使用的类型编号。
// 1=好分数学生版 2=好分数家长版 3=百分智 4=七天网络
var platformAliases = map[string]string{
	"1": "1", "student": "1", "hfs_student": "1", "hfs1": "1",
	"2": "2", "parent": "2", "hfs_parent": "2", "hfs2": "2",
	"3": "3", "bfz": "3", "baifenzi": "3",
	"4": "4", "qt": "4", "7net": "4", "qitian": "4", "seven": "4",
}

var platformLabels = map[string]string{
	"1": "好分数学生版", "2": "好分数家长版", "3": "百分智", "4": "七天网络",
}

// modeOfPlatform：/绑定账号 的类型编号 → 存储里使用的 mode 值。
var modeOfPlatform = map[string]string{
	"1": "student", "2": "parent", "3": "bfz", "4": "qt",
}

// modeLabels：存储 mode → 展示名。
var modeLabels = map[string]string{
	"student": "好分数学生版", "parent": "好分数家长版", "bfz": "百分智", "qt": "七天网络",
}

// bindingFields 构成一次绑定所需的全部字段，存档与恢复都以它为准。
var bindingFields = []string{"mode", "zh", "pw", "token", "name", "school", "grade", "xuehao", "banji", "id", "exam"}

func snapshotBinding(profile map[string]any) map[string]any {
	snap := map[string]any{}
	for _, k := range bindingFields {
		snap[k] = profile[k]
	}
	return snap
}

// applyBinding 把存档写回用户资料；存档里没有的字段显式置空，避免残留上一个平台的数据。
func applyBinding(profile map[string]any, snap map[string]any) map[string]any {
	for _, k := range bindingFields {
		if v, ok := snap[k]; ok && v != nil {
			profile[k] = v
		} else {
			profile[k] = nil
		}
	}
	return profile
}

func archiveMap(profile map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range asMap(profile["bound_accounts"]) {
		out[k] = v
	}
	return out
}

// webRequireLogin 校验登录态；未启用 OAuth 时退回默认用户。
func webRequireLogin(w http.ResponseWriter, r *http.Request) (string, bool) {
	uid := sessionUserID(r)
	if uid == "" && oauthCfg != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"ok": false, "msg": "请先登录"})
		return "", false
	}
	if uid == "" {
		uid = defaultString(os.Getenv("CLI_USER_ID"), "web-user")
	}
	return uid, true
}

func webNewContext(userID, content string) *MessageContext {
	seq := time.Now().UnixNano()
	return &MessageContext{
		Ctx:       nil,
		Body:      map[string]any{},
		Platform:  "web",
		Event:     "WEB_API",
		ID:        fmt.Sprintf("web-api-%d", seq),
		Timestamp: time.Now().Format(time.RFC3339),
		UserID:    userID,
		UserName:  userID,
		GuildID:   "web",
		ChannelID: "web",
		Seq:       fmt.Sprintf("%d", seq),
		Content:   normalizeContent(content),
	}
}

// webAuthHandler 校验登录并加载绑定资料。
func webAuthHandler(w http.ResponseWriter, userID string) (*CommandHandler, *MessageContext, bool) {
	ctx := webNewContext(userID, "")
	ctx.Ctx = nil
	h := newCommandHandler(NewWebChatSender())
	if ok, msg := h.requireAuth(ctx); !ok {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"ok": false, "msg": strings.TrimPrefix(msg, "* 错误: "), "need_bind": true,
		})
		return nil, nil, false
	}
	return h, ctx, true
}

func examToWebItem(exam map[string]any, shortID string) webExamItem {
	scoreText := asString(exam["score"])
	num, _ := strconv.ParseFloat(strings.TrimSpace(scoreText), 64)
	return webExamItem{
		GUID:      asString(exam["examGuid"]),
		Name:      defaultString(asString(exam["examName"]), "未命名考试"),
		Time:      stringOrNA(exam["time"]),
		Type:      defaultString(asString(exam["type"]), "考试"),
		Score:     scoreText,
		ScoreNum:  num,
		ShortID:   shortID,
		Published: asString(exam["examPublishType"]) != "1",
	}
}

// handleWebExams 返回考试列表（按时间倒序），供网页直接渲染。
func handleWebExams(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	userID, ok := webRequireLogin(w, r)
	if !ok {
		return
	}
	h, ctx, ok := webAuthHandler(w, userID)
	if !ok {
		return
	}
	mode := asString(h.userdata["mode"])
	if mode != "qt" {
		handleWebGenericExams(w, h, ctx, mode)
		return
	}

	exams, notes, userInfo, errMsg := h.qtLoadExamsWithAutoClaim(ctx)
	if errMsg != "" {
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"ok": false, "msg": strings.TrimPrefix(errMsg, "* 错误: "),
		})
		return
	}

	shortIDs := buildQTExamShortIDs(exams)
	items := make([]webExamItem, 0, len(exams))
	for _, exam := range exams {
		items = append(items, examToWebItem(exam, shortIDs[asString(exam["examGuid"])]))
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Time == items[j].Time {
			return items[i].Name > items[j].Name
		}
		return items[i].Time > items[j].Time
	})

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"exams":    items,
		"notes":    notes,
		"student":  defaultString(asString(userInfo["studentName"]), asString(userInfo["nickName"])),
		"school":   defaultString(asString(userInfo["schoolName"]), asString(h.userdata["school"])),
		"grade":    qtProfileGrade(userInfo),
		"total":    len(items),
		"synced_at": time.Now().Format("2006-01-02 15:04"),
	})
}

// handleWebGenericExams 好分数 / 百分智平台的考试列表。
// 这些平台返回的结构不固定，能解析出多少字段就展示多少，其余留空。
func handleWebGenericExams(w http.ResponseWriter, h *CommandHandler, ctx *MessageContext, mode string) {
	resJSON, errMsg := h.loadExamListWithRelogin(ctx)
	if errMsg != "" {
		writeJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "msg": strings.TrimPrefix(errMsg, "* 错误: ")})
		return
	}
	if resJSON == nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "msg": "未能获取考试列表"})
		return
	}
	rawList, _ := extractExamList(resJSON)
	items := make([]webExamItem, 0, len(rawList))
	for _, raw := range rawList {
		record := asMap(raw)
		id := asInt(record["examId"])
		if id == 0 {
			id = asInt(record["examid"])
		}
		name := defaultString(asString(record["name"]), defaultString(asString(record["examName"]), "未命名考试"))
		items = append(items, webExamItem{
			GUID:     fmt.Sprintf("%d", id),
			Name:     name,
			Time:     firstNonEmptyString(record, "time", "examTime", "date", "startTime", "examDate"),
			Type:     defaultString(firstNonEmptyString(record, "type", "examType"), "考试"),
			Score:    firstNonEmptyString(record, "score", "totalScore", "myScore"),
			ScoreNum: webParseScore(firstNonEmptyString(record, "score", "totalScore", "myScore")),
			ShortID:  fmt.Sprintf("%d", id),
		})
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].Time > items[j].Time })

	label := platformLabels[mode]
	if label == "" {
		label = mode
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"exams":     items,
		"notes":     []string{},
		"student":   asString(h.userdata["name"]),
		"school":    asString(h.userdata["school"]),
		"grade":     asString(h.userdata["grade"]),
		"platform":  mode,
		"platform_label": label,
		"total":     len(items),
		"synced_at": time.Now().Format("2006-01-02 15:04"),
	})
}

func firstNonEmptyString(record map[string]any, keys ...string) string {
	for _, k := range keys {
		if v := asString(record[k]); v != "" {
			return v
		}
	}
	return ""
}

func webParseScore(s string) float64 {
	v, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return v
}

// handleWebUnclaimed 返回七天网络里尚未认领的考试（官方接口 getUnClaimExams）。
func handleWebUnclaimed(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	userID, ok := webRequireLogin(w, r)
	if !ok {
		return
	}
	h, ctx, ok := webAuthHandler(w, userID)
	if !ok {
		return
	}
	if asString(h.userdata["mode"]) != "qt" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "msg": "仅七天网络支持认领考试"})
		return
	}
	res, _, errMsg := h.qtExecuteWithProfile(ctx, "get_unclaim_exams", func(token string, ui map[string]any) map[string]any {
		return qtGetUnClaimExamsWithContext(ctx, token, ui)
	})
	if errMsg != "" {
		writeJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "msg": strings.TrimPrefix(errMsg, "* 错误: ")})
		return
	}
	type unclaimedItem struct {
		GUID       string   `json:"guid"`
		Name       string   `json:"name"`
		Time       string   `json:"time"`
		SchoolName string   `json:"school_name"`
		Candidates []string `json:"candidates"`
	}
	out := make([]unclaimedItem, 0)
	for _, item := range qtFlattenUnclaimedExams(res["data"]) {
		cands := make([]string, 0)
		for _, c := range asSlice(item["studentCodeList"]) {
			code := asString(c)
			if code != "" {
				cands = append(cands, code)
			}
		}
		out = append(out, unclaimedItem{
			GUID:       asString(item["examGuid"]),
			Name:       defaultString(asString(item["examName"]), "未命名考试"),
			Time:       stringOrNA(item["time"]),
			SchoolName: defaultString(asString(item["schoolName"]), ""),
			Candidates: cands,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "items": out, "total": len(out)})
}

// handleWebClaim 认领指定的未认领考试（官方接口 claimExam）。
func handleWebClaim(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "msg": "仅支持 POST 请求"})
		return
	}
	userID, ok := webRequireLogin(w, r)
	if !ok {
		return
	}
	var req struct {
		ExamGUID    string `json:"exam_guid"`
		StudentCode string `json:"student_code"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "msg": "请求体解析失败"})
		return
	}
	req.ExamGUID = strings.TrimSpace(req.ExamGUID)
	req.StudentCode = strings.TrimSpace(req.StudentCode)
	if req.ExamGUID == "" || req.StudentCode == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "msg": "缺少考试标识或学号"})
		return
	}
	h, ctx, ok := webAuthHandler(w, userID)
	if !ok {
		return
	}
	if asString(h.userdata["mode"]) != "qt" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "msg": "仅七天网络支持认领考试"})
		return
	}
	res, _, errMsg := h.qtExecuteWithProfile(ctx, fmt.Sprintf("claim_exam:exam_guid=%s", req.ExamGUID),
		func(token string, ui map[string]any) map[string]any {
			return qtClaimExamWithContext(ctx, token, req.ExamGUID, req.StudentCode)
		})
	if errMsg != "" {
		writeJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "msg": strings.TrimPrefix(errMsg, "* 错误: ")})
		return
	}
	if asInt(res["status"]) != 0 && asInt(res["status"]) != 200 {
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"ok": false, "msg": defaultString(asString(res["message"]), "认领失败"),
		})
		return
	}
	opDeleteQTStudentExamCache(userID)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleWebExamDetail 返回单场考试的科目明细；科目接口不可用时回退为总分概览。
func handleWebExamDetail(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	userID, ok := webRequireLogin(w, r)
	if !ok {
		return
	}
	guid := strings.TrimSpace(r.URL.Query().Get("guid"))
	if guid == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "msg": "缺少考试标识"})
		return
	}
	h, ctx, ok := webAuthHandler(w, userID)
	if !ok {
		return
	}
	if asString(h.userdata["mode"]) != "qt" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "msg": "当前绑定平台不支持该查询。"})
		return
	}

	exams, userInfo, errMsg := h.qtFetchAllClaimedExams(ctx)
	if errMsg != "" {
		writeJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "msg": strings.TrimPrefix(errMsg, "* 错误: ")})
		return
	}
	shortIDs := buildQTExamShortIDs(exams)
	var target map[string]any
	for _, exam := range exams {
		if asString(exam["examGuid"]) == guid {
			target = exam
			break
		}
	}
	if target == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "msg": "未找到该考试，请刷新列表后重试。"})
		return
	}

	subjectsRes, _, detailErr := h.qtExecuteWithProfile(ctx, "get_question_subjects", func(token string, ui map[string]any) map[string]any {
		return qtGetQuestionSubjectsWithContext(ctx, token, ui, target)
	})

	subjects := make([]map[string]any, 0)
	var subjectsData map[string]any
	if detailErr == "" && asInt(subjectsRes["status"]) == 0 {
		decoded, err := decryptECBJSON(qtAESKey, asString(asMap(subjectsRes["data"])["content"]))
		if err == nil {
			subjectsData = asMap(decoded)
			for index, subject := range qtVisibleSubjects(subjectsData) {
				subjects = append(subjects, map[string]any{
					"id":        fmt.Sprintf("%03d", index+1),
					"name":      stringOrNA(subject["km"]),
					"score":     stringOrNA(subject["myScore"]),
					"full":      stringOrNA(subject["fullScore"]),
					"has_card":  asString(asMap(subject["question"])["asiresponse"]) != "",
				})
			}
		}
	}

	payload := map[string]any{
		"ok":      true,
		"exam":    examToWebItem(target, shortIDs[guid]),
		"student": qtProfileStudentName(userInfo),
	}
	if len(subjects) > 0 {
		payload["subjects"] = subjects
		payload["subject_total"] = qtTotalSubject(subjectsData)
		payload["detail_available"] = true
	} else {
		payload["subjects"] = []map[string]any{}
		payload["detail_available"] = false
		msg := "科目明细暂不可用"
		if detailErr != "" {
			msg = strings.TrimPrefix(detailErr, "* 错误: ")
		} else if asString(subjectsRes["message"]) != "" {
			msg = asString(subjectsRes["message"])
		}
		payload["detail_error"] = msg
	}
	writeJSON(w, http.StatusOK, payload)
}

// handleWebBind 绑定查分平台账号（账号与密码保存在服务器）。
func handleWebBind(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "msg": "仅支持 POST 请求"})
		return
	}
	userID, ok := webRequireLogin(w, r)
	if !ok {
		return
	}
	var req webBindRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "msg": "请求体解析失败"})
		return
	}
	req.Account = strings.TrimSpace(req.Account)
	req.Password = strings.TrimSpace(req.Password)
	rawPlatform := strings.TrimSpace(defaultString(req.Platform, "4"))
	ptype, known := platformAliases[strings.ToLower(rawPlatform)]
	if !known {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"ok": false, "msg": "未知的平台类型：" + rawPlatform,
		})
		return
	}
	if req.Account == "" || req.Password == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "msg": "请填写账号和密码"})
		return
	}

	// 绑定流程会重建用户记录（opDelete），先把 OAuth 资料与已有绑定存档留住，成功后写回。
	before := opView(userID)
	oldMode := asString(before["mode"])
	archives := archiveMap(before)
	oauthKeep := map[string]any{}
	for k, v := range before {
		if strings.HasPrefix(k, "oauth_") {
			oauthKeep[k] = v
		}
	}
	// 绑定另一个平台时，把当前平台存档，而不是覆盖掉。
	if oldMode != "" && oldMode != modeOfPlatform[ptype] {
		archives[oldMode] = snapshotBinding(before)
	}

	ctx := webNewContext(userID, fmt.Sprintf("/绑定账号 %s %s %s", ptype, req.Account, req.Password))
	h := newCommandHandler(NewWebChatSender())
	msg := h.bindAccount(ctx)
	if strings.Contains(msg, "* 错误") {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"ok": false, "msg": strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(msg, "* 错误: "), "* 错误:")),
		})
		return
	}

	after := opView(userID)
	for k, v := range oauthKeep {
		after[k] = v
	}
	delete(archives, asString(after["mode"]))
	after["bound_accounts"] = archives
	opWrite(userID, after)
	opDeleteQTStudentExamCache(userID)

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":             true,
		"platform":       ptype,
		"mode":           asString(after["mode"]),
		"platform_label": platformLabels[ptype],
		"name":           asString(after["name"]),
		"school":         asString(after["school"]),
		"grade":          asString(after["grade"]),
		"account":        asString(after["zh"]),
		"saved_accounts": webAccountSummaries(opView(userID)),
		"message":        strings.TrimSpace(msg),
	})
}

// webAccountSummaries 列出当前绑定 + 已存档的其它平台账号。
func webAccountSummaries(profile map[string]any) []map[string]any {
	out := make([]map[string]any, 0)
	currentMode := asString(profile["mode"])
	if currentMode != "" {
		out = append(out, map[string]any{
			"mode":    currentMode,
			"label":   defaultString(modeLabels[currentMode], currentMode),
			"account": asString(profile["zh"]),
			"name":    asString(profile["name"]),
			"school":  asString(profile["school"]),
			"current": true,
		})
	}
	for mode, raw := range asMap(profile["bound_accounts"]) {
		if mode == currentMode {
			continue
		}
		snap := asMap(raw)
		out = append(out, map[string]any{
			"mode":    mode,
			"label":   defaultString(modeLabels[mode], mode),
			"account": asString(snap["zh"]),
			"name":    asString(snap["name"]),
			"school":  asString(snap["school"]),
			"current": false,
		})
	}
	return out
}

// handleWebSwitch 在已绑定的多个平台账号之间切换，无需重新输入密码。
func handleWebSwitch(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "msg": "仅支持 POST 请求"})
		return
	}
	userID, ok := webRequireLogin(w, r)
	if !ok {
		return
	}
	var req struct {
		Mode string `json:"mode"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "msg": "请求体解析失败"})
		return
	}
	target := strings.TrimSpace(req.Mode)
	if target == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "msg": "缺少目标平台"})
		return
	}

	profile := opView(userID)
	currentMode := asString(profile["mode"])
	if currentMode == target {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "mode": target, "accounts": webAccountSummaries(profile)})
		return
	}
	archives := archiveMap(profile)
	snap, exists := archives[target]
	if !exists {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "msg": "尚未绑定该平台账号"})
		return
	}

	// 当前账号存档，目标账号恢复为主绑定。
	if currentMode != "" {
		archives[currentMode] = snapshotBinding(profile)
	}
	next := map[string]any{}
	for k, v := range profile {
		next[k] = v
	}
	next = applyBinding(next, asMap(snap))
	delete(archives, target)
	next["bound_accounts"] = archives
	opWrite(userID, next)
	opDeleteQTStudentExamCache(userID)

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"mode":     target,
		"label":    defaultString(modeLabels[target], target),
		"name":     asString(asMap(snap)["name"]),
		"school":   asString(asMap(snap)["school"]),
		"account":  asString(asMap(snap)["zh"]),
		"accounts": webAccountSummaries(opView(userID)),
	})
}

// handleWebUnbind 解除绑定并清除服务器上保存的账号密码。
func handleWebUnbind(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "msg": "仅支持 POST 请求"})
		return
	}
	userID, ok := webRequireLogin(w, r)
	if !ok {
		return
	}
	profile := opView(userID)
	currentMode := asString(profile["mode"])
	kept := map[string]any{}
	for k, v := range profile {
		if strings.HasPrefix(k, "oauth_") || k == "bound_accounts" {
			kept[k] = v
			continue
		}
		drop := false
		for _, f := range bindingFields {
			if k == f {
				drop = true
				break
			}
		}
		if drop {
			continue
		}
		kept[k] = v
	}
	opWrite(userID, kept)
	opDeleteQTStudentExamCache(userID)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"mode":     currentMode,
		"accounts": webAccountSummaries(opView(userID)),
	})
}

// handleWebProfile 返回当前绑定状态，供设置页展示。
func handleWebProfile(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	userID, ok := webRequireLogin(w, r)
	if !ok {
		return
	}
	profile := opView(userID)
	mode := asString(profile["mode"])
	label := "未绑定"
	if mode != "" {
		label = defaultString(modeLabels[mode], mode)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"bound":      mode != "",
		"mode":       mode,
		"mode_label": label,
		"account":    asString(profile["zh"]),
		"name":       asString(profile["name"]),
		"school":     asString(profile["school"]),
		"grade":      asString(profile["grade"]),
		"accounts":   webAccountSummaries(profile),
	})
}
