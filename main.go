package main

import (
	"context"
	"log"
	"os"
	"strings"

	"github.com/aliyun/fc-runtime-go-sdk/fc"
)

func main() {
	configureRuntimeFromEnv()
	switch selectedChatAdapter() {
	case "web":
		if err := runWebServer(context.Background()); err != nil {
			log.Fatal(err)
		}
		return
	case "fc":
		fc.Start(HandleRequest)
		return
	default:
		if err := runCLIChat(context.Background()); err != nil {
			log.Fatal(err)
		}
		return
	}
}

func selectedChatAdapter() string {
	return strings.ToLower(strings.TrimSpace(os.Getenv("CHAT_ADAPTER")))
}

func configureRuntimeFromEnv() {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("DATA_STORE"))) {
	case "memory":
		dataStore = NewMemoryStore()
	case "json", "":
		dataStore = NewJSONStore()
	case "sqlite":
		dataStore = NewSQLiteStore()
	case "mysql":
		dataStore = MySQLStore{}
	default:
		log.Printf("unknown DATA_STORE=%q, using JSONStore", os.Getenv("DATA_STORE"))
		dataStore = NewJSONStore()
	}
}
