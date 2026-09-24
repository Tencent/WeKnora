package main

import (
	"embed"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
)

//go:embed web/*
var webAssets embed.FS

func main() {
	workspace := flag.String("workspace", "./local/evaluation-datasets", "本地数据集工作目录")
	addr := flag.String("addr", "127.0.0.1:8090", "监听地址（必须为 loopback 地址）")
	flag.Parse()

	if !isLoopbackAddress(*addr) {
		fmt.Fprintln(os.Stderr, "--addr 必须使用 127.0.0.1、localhost 或 ::1")
		os.Exit(2)
	}
	store, err := newProjectStore(*workspace)
	if err != nil {
		log.Fatalf("初始化工作目录失败：%v", err)
	}
	app, err := newStudioServer(store, webAssets)
	if err != nil {
		log.Fatalf("初始化服务失败：%v", err)
	}
	log.Printf("WeKnora Dataset Studio 已启动：http://%s", *addr)
	log.Printf("数据目录：%s", store.root)
	if err := http.ListenAndServe(*addr, app); err != nil {
		log.Fatal(err)
	}
}
