package main

import (
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/patrickmn/go-cache"
)

// 检查是否跨域，默认都支持
func checkOrigin(*http.Request) bool {
	return true
}

// websocket upgrade的参数配置
var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,        // 读数据的大小
	WriteBufferSize: 1024,        // 写数据的大小
	CheckOrigin:     checkOrigin, //检查是否跨域
}

// web服务
func server() {
	// 初始化cache
	GoCache = cache.New(cache.NoExpiration, cache.NoExpiration) // 初始化共享内存

	InitPlay() // 加载警告文件

	r := mux.NewRouter()
	r.HandleFunc("/ws", pdWs)                      // websocket
	r.HandleFunc("/display", pdDisplay)            // 错误展示
	r.HandleFunc("/history", pdHistory)            // 历史记录
	r.HandleFunc("/check/{code}", pdCheck)         // 检查捆码
	r.HandleFunc("/upload/{id}/{code}", pdUpload)  // 上传
	r.HandleFunc("/lookup/{id}/{code}", pdLookup)  // 查看
	r.HandleFunc("/fail/{code}", pdFail)           // 失败
	r.HandleFunc("/failRecord/{id}", pdFailRecord) // 单条失败率
	r.HandleFunc("/filter/{code}", pdFilter)       // 过滤失败标签
	r.HandleFunc("/remove/{code}", pdRemove)       // 整版移除
	r.HandleFunc("/delete/{id}/{code}", pdDelete)  // 删除记录  物理删除

	addr := fmt.Sprintf("%s:%d", Ini.Service.Ip, Ini.Service.Port)
	err := http.ListenAndServe(addr, r) // 启动服务
	if err != nil {
		log.Fatal(err)
		return
	}
}

func init() {
	Ini = ReadConfig()
	file := "log.txt"
	logFile, err := os.OpenFile(file, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0766)
	if err != nil {
		panic(err)
	}
	log.SetOutput(logFile)
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.Println("程序的配置是: ", Ini)
}

func main() {
	log.Println("程序开始启动...")
	server()
}
