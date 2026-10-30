package main

import (
	"context"
	"io"
)

// WebChatSender 收集一次命令执行过程中产生的文本与图片，
// 由 HTTP handler 统一序列化为 JSON 返回给网页前端。
// 与 CLIChatSender / QQChatSender 平级，实现同一个 ChatSender 接口。
type WebChatSender struct {
	Texts  []string
	Images []WebImage
}

// WebImage 是返回给前端的图片负载（base64 data URL）。
type WebImage struct {
	Name string `json:"name"`
	Data string `json:"data"`
}

func NewWebChatSender() *WebChatSender {
	return &WebChatSender{}
}

func (s *WebChatSender) SendText(ctx context.Context, msg *MessageContext, content string) map[string]any {
	if content == "" {
		return map[string]any{"code": 0}
	}
	s.Texts = append(s.Texts, content)
	return map[string]any{"code": 0}
}

func (s *WebChatSender) SendImage(ctx context.Context, msg *MessageContext, imageContent []byte, content string) map[string]any {
	if len(imageContent) == 0 {
		return map[string]any{"error": "empty image content"}
	}
	s.Images = append(s.Images, WebImage{
		Name: content,
		Data: encodeWebImageDataURL(imageContent),
	})
	return map[string]any{"code": 0}
}

func (s *WebChatSender) SendImageReader(ctx context.Context, msg *MessageContext, imageContent io.Reader, content string) map[string]any {
	if imageContent == nil {
		return map[string]any{"error": "image reader is nil"}
	}
	data, err := io.ReadAll(imageContent)
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	return s.SendImage(ctx, msg, data, content)
}
