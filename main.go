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

	r := mux.NewRouter()
	r.HandleFunc("/ws", pdWs).Methods(http.MethodGet, http.MethodOptions)                      // websocket
	r.HandleFunc("/display", pdDisplay).Methods(http.MethodGet, http.MethodOptions)            // 错误展示
	r.HandleFunc("/history", pdHistory).Methods(http.MethodGet, http.MethodOptions)            // 历史记录
	r.HandleFunc("/check/{code}", pdCheck).Methods(http.MethodGet, http.MethodOptions)         // 检查捆码
	r.HandleFunc("/uploadBefore", pdUploadBefore).Methods(http.MethodGet, http.MethodOptions)  //  上传确认
	r.HandleFunc("/upload/{id}/{code}", pdUpload).Methods(http.MethodGet, http.MethodOptions)  // 上传
	r.HandleFunc("/lookup/{id}/{code}", pdLookup).Methods(http.MethodGet, http.MethodOptions)  // 查看
	r.HandleFunc("/fail/{code}", pdFail).Methods(http.MethodGet, http.MethodOptions)           // 失败
	r.HandleFunc("/failRecord/{id}", pdFailRecord).Methods(http.MethodGet, http.MethodOptions) // 单条失败率
	r.HandleFunc("/filter/{code}", pdFilter).Methods(http.MethodGet, http.MethodOptions)       // 过滤失败标签
	r.HandleFunc("/remove/{code}", pdRemove).Methods(http.MethodGet, http.MethodOptions)       // 整版移除
	r.HandleFunc("/delete/{id}/{code}", pdDelete).Methods(http.MethodGet, http.MethodOptions)  // 删除记录  物理删除
	r.Use(mux.CORSMethodMiddleware(r))                                                         // 设置跨域支持

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
	//t1()
	//t2()
	//t3()
	//t5()
	//t6()
}

//  windows 编译没有窗口
//  go build -ldflags "-H windowsgui"
