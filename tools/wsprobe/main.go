// wsprobe：WebSocket 冒烟测试客户端
// 用法: go run ./tools/wsprobe -addr localhost:8090 -session <sessionID> [-token <t>] [-msg "问题"] [-image <png路径>]
package main

import (
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

func main() {
	addr := flag.String("addr", "localhost:8090", "服务地址")
	sessionID := flag.String("session", "", "会话 ID")
	token := flag.String("token", "", "认证 token")
	msg := flag.String("msg", "你好", "发送的消息")
	imagePath := flag.String("image", "", "可选：要发送的图片文件路径")
	flag.Parse()

	if *sessionID == "" {
		fmt.Fprintln(os.Stderr, "missing -session")
		os.Exit(1)
	}

	wsURL := fmt.Sprintf("ws://%s/ws/%s", *addr, *sessionID)
	if *token != "" {
		wsURL += "?token=" + url.QueryEscape(*token)
	}
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dial: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	payload := map[string]any{"type": "user_message", "content": *msg}
	if *imagePath != "" {
		raw, err := os.ReadFile(*imagePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "read image: %v\n", err)
			os.Exit(1)
		}
		mime := "image/png"
		if strings.HasSuffix(strings.ToLower(*imagePath), ".jpg") || strings.HasSuffix(strings.ToLower(*imagePath), ".jpeg") {
			mime = "image/jpeg"
		}
		payload["images"] = []map[string]string{{
			"mimeType": mime,
			"data":     base64.StdEncoding.EncodeToString(raw),
		}}
		fmt.Printf("--- sending image %s (%d bytes) ---\n", *imagePath, len(raw))
	}
	conn.WriteJSON(payload)

	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		conn.SetReadDeadline(deadline)
		_, data, err := conn.ReadMessage()
		if err != nil {
			fmt.Fprintf(os.Stderr, "read: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(string(data))

		var ev struct {
			Type  string `json:"type"`
			Chunk *struct {
				Type string `json:"type"`
			} `json:"chunk"`
		}
		if json.Unmarshal(data, &ev) == nil {
			if ev.Type == "chunk" && ev.Chunk != nil && ev.Chunk.Type == "done" {
				fmt.Println("--- PROBE_OK: round trip complete ---")
				return
			}
			if ev.Type == "error" {
				fmt.Println("--- PROBE_FAIL ---")
				os.Exit(1)
			}
		}
	}
	fmt.Println("--- PROBE_TIMEOUT ---")
	os.Exit(1)
}
