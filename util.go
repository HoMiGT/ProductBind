/*
@Time : 2022/2/10 1:35 下午
@Author : houmin
@File : util
@Software: GoLand
*/
package main

import (
	"bufio"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-gota/gota/dataframe"
	"github.com/go-gota/gota/series"
	"github.com/gorilla/websocket"
	"github.com/hajimehoshi/oto"
	"github.com/patrickmn/go-cache"
	"github.com/tosone/minimp3"
)

// StartHandle 开始
func StartHandle(conn *websocket.Conn, csvChan chan []byte, warnChan chan struct{}, quitReaderChan chan struct{},
	quitWriterChan chan struct{}, quitWarnChan chan struct{}, wp *WsParams, wc *WsCount) {

	// 判断文件夹是否存在
	fd := filepath.Join("static", wp.BundleCode)

	err := DirNotExistsCreate(fd) // 判断文件夹是否存在 不存在则创建,创建失败则报错
	if err != nil {
		log.Println("error:", "创建'", fd, "'文件夹失败:", err)
		err = conn.WriteJSON(WsResponse{
			OperateCode: OperaArea,
			Response: Response{
				Status: false,
				Msg:    err.Error(),
			},
		})
		log.Println("error:", "websocket 发送给客户端错误: ", err)
		return
	}

	var id int
	// 新建记录
	crr := CodeReadRecords{}
	records := crr.Select(0, 0, 0, wp.BundleCode, 3)
	if len(records) > 0 {
		// 根据捆码查询 如有过则更新文件
		id = int(records[0].ID)
		crr.UpdateStatus(id, "", "", 4)
	} else {
		// 否则新增记录 并新建文件
		current := Now()
		id = crr.Insert(wp.BundleCode, wp.IsIntoBox, current)
	}

	log.Println("info:", wp.BundleCode, ",捆码的记录创建成功")
	if err = conn.WriteJSON(WsResponse{
		OperateCode: OperaArea,
		Response: Response{
			Status: true,
			Data:   QuerySignal{IsQuery: true},
		},
	}); err != nil {
		log.Println("error:", "websocket 发送给客户端错误: ", err)
	}
	if id > 0 { // 插入记录成功 开启任务协程
		// 开启socket读摄像头传输的数据
		go Reader(conn, csvChan, quitReaderChan, warnChan, wp)
		// 开启写入
		go Writer(conn, csvChan, quitWriterChan, wp, wc, fd)
		// 开启警告的  定时器检测30s是否有数据写入
		go Warning(warnChan, quitWarnChan, wp)

		// 更改状态 让读写协程开始工作
		wp.SetKeep(true)
	}
	log.Println("info:", "状态: 已开始")
}

// StopHandle 停止
func StopHandle(wp *WsParams) {
	// 更新状态 停止状态
	wp.SetKeep(false)
	// 退出协程状态
	wp.SetStop()
	log.Println("info:", "状态: 已停止")
}

// ContinueHandle 继续
func ContinueHandle(wp *WsParams, wc *WsCount) {
	// 更新状态 继续状态
	wp.SetKeep(true)
	wc.ReSetValue()
	log.Println("info:", "状态: 已继续")
}

// SuspendHandle 暂停
func SuspendHandle(wp *WsParams) {
	// 更新停止状态
	wp.SetKeep(false)
	log.Println("info:", "状态: 已暂停")
}

// Reader websocket读取并发送数据
func Reader(conn *websocket.Conn, csvChan chan []byte, quitReaderChan <-chan struct{}, warnChan chan<- struct{}, wp *WsParams) {
	log.Println("info:开始启动读协程...")
	addr := fmt.Sprintf("%s:%d", Ini.Camera.Ip, Ini.Camera.Port)
	repeat := 0
	var e string
QUIT:
	for {
		if !wp.GetKeep() { // 没有开始状态时 要先等待状态修改
			time.Sleep(time.Millisecond * 300)
			continue
		}
		client, err := net.Dial("tcp", addr) // 连接摄像头的网络端口
		if err != nil {
			e = "error:连接摄像头错误: " + err.Error()
			log.Println(e)
			err = conn.WriteJSON(WsResponse{
				OperateCode: OperaArea,
				Response: Response{
					Status: false,
					Msg:    e,
				},
			}) // 异常发送错误信息
			if err != nil {
				log.Println("error:", err)
			}
			if repeat < 3 { // 重复尝试连接3次
				time.Sleep(time.Second * 1)
				repeat += 1
				continue
			} else {
				break QUIT
			}
		}
		_, _ = client.Write([]byte("SKCLOSE\r"))
		wp.close = client.Close
		reader := bufio.NewReader(client) // 实例化连接客户端的读句柄

		for {
			if wp.Stop() {
				break QUIT
			}
			select {
			case <-quitReaderChan: // 退出读协程
				break QUIT
			default:
				{
					msg, err := reader.ReadBytes('\n') // 读取传输的数据
					if err == io.EOF {
						e = "error:摄像头断开连接，摄像头返回信息:" + err.Error()
						log.Println(e)
						_ = conn.WriteJSON(WsResponse{
							OperateCode: OperaArea,
							Response: Response{
								Status: false,
								Msg:    e,
							},
						})
						break QUIT
					}
					if err != nil {
						log.Println("error:", err)
						continue
					}
					if !wp.GetKeep() { // 判断是否是暂停或者停止状态
						continue
					}
					csvChan <- msg
					warnChan <- struct{}{} // 发送更新时间信号
				}
			}
		}
	}
	log.Println("info:读协程退出")
	close(csvChan)  // 关闭csv通道
	close(warnChan) // 关闭警告通道
	log.Println("info:已关闭 csvChan与warnChan 通道")
}

// Writer 写入csv文件
func Writer(conn *websocket.Conn, csvChan <-chan []byte, quitWriterChan <-chan struct{}, wp *WsParams, wc *WsCount, fd string) {
	log.Println("info:开始启动写协程...")
	var (
		e              string
		offset, cursor int
		status         bool
		err            error
	)
	// 新建成功文件，新建失败文件
	// 保存成功文件
	fs := filepath.Join(fd, SucFile)
	if !PathExists(fs) { // 判断成功文件是否存在，不存在创建文件
		_, err = os.Create(fs)
		if err != nil {
			e = "error:" + err.Error()
			log.Println(e)
			err = conn.WriteJSON(WsResponse{
				OperateCode: OperaArea,
				Response: Response{
					Status: false,
					Msg:    e,
				},
			})
			if err != nil {
				log.Println(err)
			}
			wp.SetKeep(false)
			return
		}
	} else {
		// 读取文件获取最大的版索引
		offset, err = FindMaxId(fs)
		if err != nil {
			e = "error:" + err.Error()
			log.Println(e)
			err = conn.WriteJSON(WsResponse{
				OperateCode: OperaArea,
				Response: Response{
					Status: false,
					Msg:    e,
				},
			})
			if err != nil {
				log.Println(err)
			}
			wp.SetKeep(false)
			return
		}
	}
	fsh, err := os.OpenFile(fs, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644) // 创建写入成功的文件句柄
	if err != nil {
		e = "error:" + err.Error()
		log.Println(e)
		err = conn.WriteJSON(WsResponse{
			OperateCode: OperaArea,
			Response: Response{
				Status: false,
				Msg:    e,
			},
		})
		if err != nil {
			log.Println("error:", err)
		}
		wp.SetKeep(false)
		return
	}

	// 保存失败文件
	ff := filepath.Join(fd, FaiFile)
	if !PathExists(ff) { // 判断失败文件是否存在，不存在创建文件
		_, err = os.Create(ff)
		if err != nil {
			e = "error:" + err.Error()
			log.Println(e)
			err = conn.WriteJSON(WsResponse{
				OperateCode: OperaArea,
				Response: Response{
					Status: false,
					Msg:    e,
				},
			})
			if err != nil {
				log.Println("error:", err)
			}
			wp.SetKeep(false)
			return
		}
	}
	ffh, err := os.OpenFile(ff, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644) // 创建写入失败的文件句柄
	if err != nil {
		e = "error:" + err.Error()
		log.Println(e)
		err = conn.WriteJSON(WsResponse{
			OperateCode: OperaArea,
			Response: Response{
				Status: false,
				Msg:    e,
			},
		})
		if err != nil {
			log.Println("error:", err)
		}
		wp.SetKeep(false)
		return
	}

	fsw := csv.NewWriter(fsh) // 创建成功csv的writer对象
	ffw := csv.NewWriter(ffh) // 创建失败csv的writer对象

	defer func() {
		log.Println("info:写协程退出")
		_ = fsh.Close()
		_ = ffh.Close()
		log.Println("info:csv对象 fsw,ffw,player 资源已经关闭")

	}()

	log.Println("info:开始加载自定义规则...")
	rule := ReadRules() // 读取配置的识别规则
	log.Println("info:自定义规则加载成功")

	sucKey := wp.BundleCode + GoKeySucSuffix
	faiKey := wp.BundleCode + GoKeyFaiSuffix
	log.Println("info:开始清空缓存...")
	GoCache.Delete(sucKey) // 删除成功文件缓存
	GoCache.Delete(faiKey) // 删除失败文件缓存
	log.Println("info:缓存清空成功")
	for {
		if wp.Stop() {
			return
		}
		select {
		case <-quitWriterChan: // 退出写协程
			return
		case r := <-csvChan:
			{
				if !wp.GetKeep() { // 判断是否是暂停或停止
					continue
				}
				if r == nil {
					continue
				}
				s := string(r)
				s = strings.Replace(s, CameraReplace, "", -1)
				ok, dl, err := ParseText(s, fsw, ffw, wp.Limit, wp.IsIntoBox, rule, cursor, offset)
				if err != nil {
					e = "error:" + err.Error()
					log.Println(e)
					err = conn.WriteJSON(WsResponse{
						OperateCode: OperaArea,
						Response: Response{
							Status: false,
							Msg:    e,
						},
					})
					if err != nil {
						log.Println("error:", err)
					}
					continue
				}
				if ok {
					wc.SetValue() // 设置成功的值
					status = true
				} else {
					wc.SetValueFail() // 设置失败的值
					status = false
					GoCache.Delete(GoKeyFailParse)
					GoCache.Set(GoKeyFailParse, dl, cache.NoExpiration) // 设置错误的缓存值
				}
				err = conn.WriteJSON(WsResponse{ // 向客户端发送数据
					OperateCode: OperaArea,
					Response: Response{
						Status: status,
						Msg:    "",
						Data:   wc.GetValue(),
					},
				})
				if !status {
					for i := 0; i < 3; i++ {
						if !wp.GetKeep() {
							break
						}
						PlayMp3(wp.WarnBytes)
						time.Sleep(time.Second * 1)
					}
				}
				if err != nil {
					log.Println("error", err)
				}
				cursor += 1
			}
		}
	}
}

// Warning 播放音频文件
func Warning(warnChan <-chan struct{}, quitWarnChan <-chan struct{}, wp *WsParams) {
	log.Println("info:开始启动写协程...")
	defer func() {
		log.Println("info:警告协程退出")
		log.Println("info:播放资源已释放")
	}()

	var begin time.Time // 开始时间
	for {
		if wp.Stop() {
			return
		}
		select {
		case <-quitWarnChan: // 退出警告协程
			return
		case <-warnChan: // 警告协程更新时间
			begin = time.Now()
		default:
			{
				if !wp.GetKeep() { // 判断是否开始
					time.Sleep(time.Millisecond * 300)
					begin = time.Now()
					continue
				}
				if begin.IsZero() { // 判断开始时间是否初始化，没有则进行更新时间
					begin = time.Now()
				}
				current := time.Now()
				if current.Sub(begin).Seconds() > 30.0 {
					PlayMp3(wp.TipsBytes)
					time.Sleep(time.Second * 1) // 警告隔1s
				}
			}
		}

	}
}

// PlayMp3 播放mp3
func PlayMp3(mp3 []byte) {
	dec, data, err := minimp3.DecodeFull(mp3)
	if err != nil { // 解码
		log.Println(err)
		return
	}
	if Ctx == nil {
		Ctx, err = oto.NewContext(dec.SampleRate, dec.Channels, 2, 1024)
		if err != nil {
			log.Println(err)
			return
		}
	}

	play := Ctx.NewPlayer()
	_, _ = play.Write(data)
	defer func() {
		_ = play.Close()
	}()
}

// ParseUrl 解析url
func ParseUrl(u string) string {
	if strings.Contains(u, "?") { // 判断链接里是否包含？
		return strings.Split(u, "?")[1]
	} else if strings.Contains(u, "=") { // 判断链接里是否包含 =
		return strings.Split(u, "=")[1]
	} else if strings.Contains(u, ":") {
		return strings.Split(u, ":")[1]
	} else { // 否则直接返回
		return u
	}
}

// DefaultParse 默认解析
func DefaultParse(s string) string {
	if strings.Contains(s, "?") {
		return strings.Split(s, "?")[1]
	} else if strings.Contains(s, "=") {
		return strings.Split(s, "=")[1]
	} else if strings.Contains(s, ":") {
		return strings.Split(s, ":")[1]
	} else {
		return s
	}
}

// ParseSlice 解析列表
func ParseSlice(l []string, cs []string, r *Rule, isIntoBox int, isBox bool) []string {
	if len(r.IndexPosition) > 0 {
		csl := len(cs)
		for _, i := range r.IndexPosition {
			if i >= csl {
				continue
			}
			t := cs[i]
			t = ParseUrl(t)
			l = append(l, t)
		}
	} else if r.StartWith != "" {
		for i := range cs {
			t := cs[i]
			if strings.HasPrefix(t, r.StartWith) {
				t = ParseUrl(t)
				l = append(l, t)
			}
		}
	} else if r.EndWith != "" {
		for i := range cs {
			t := cs[i]
			if strings.HasSuffix(t, r.EndWith) {
				t = ParseUrl(t)
				l = append(l, t)
			}
		}
	} else if r.Contain != "" {
		for i := range cs {
			t := cs[i]
			if strings.Contains(t, r.Contain) {
				t = ParseUrl(t)
				l = append(l, t)
			}
		}
	} else {
		if isIntoBox == 1 {
			if isBox {
				l = append(l, DefaultParse(cs[0]))
			} else {
				tmp := cs[1:]
				for i := range tmp {
					cd := tmp[i]
					l = append(l, DefaultParse(cd))
				}
			}
		} else {
			if isBox {
				l = nil
			} else {
				for i := range cs {
					cd := cs[i]
					l = append(l, DefaultParse(cd))
				}
			}
		}
	}
	return l
}

// ParseText 解析摄像头传输的数据
func ParseText(s string, fsw *csv.Writer, ffw *csv.Writer, limit int, isIntoBox int, rule *Rules, cursor, offset int) (bool, [][]string, error) {
	s = strings.TrimSpace(s)
	cs := strings.Split(s, ",")
	l := len(cs)
	bs := make([]string, 0, l) // 箱码切片
	ls := make([]string, 0, l) // 标签切片

	bs = ParseSlice(bs, cs, &rule.Box, isIntoBox, true)    // 解析box
	ls = ParseSlice(ls, cs, &rule.Label, isIntoBox, false) // 解析label

	bi := strings.Join(bs, ",") // box信息

	lsl := len(ls)
	isQualified := lsl == limit // 是否合格

	var wc map[string]string
	var err error
	if !isQualified {
		if lsl == 0 {
			return false, nil, errors.New("解析的label列表的长度为0")
		}
		wc, err = QueryWasteCode(ls)
		if err != nil {
			return false, nil, err
		}
	}
	insert := make([][]string, lsl, lsl) // 写入文件切片
	for i := range insert {
		switch isQualified { // 合格
		case true:
			if isIntoBox == 1 { // 装箱
				insert[i] = make([]string, 4, 4)
			} else {
				insert[i] = make([]string, 3, 3)
			}
		case false:
			insert[i] = make([]string, 4, 4)
		}
	}
	scanTime := Now()
	for i := range ls {
		index := strconv.Itoa(cursor + offset)
		switch isQualified {
		case true: // 合格
			if isIntoBox == 1 { // 装箱
				insert[i][0] = index    // 版索引
				insert[i][1] = ls[i]    // 标签码
				insert[i][2] = scanTime // 扫码时间
				insert[i][3] = bi       // 箱码
			} else { // 不装箱
				insert[i][0] = index    // 版索引
				insert[i][1] = ls[i]    // 标签码
				insert[i][2] = scanTime // 扫码时间
			}
		case false: // 不合格
			{
				c := ls[i]
				w := wc[c]
				insert[i][0] = index    // 版索引
				insert[i][1] = c        // 数码
				insert[i][2] = w        // 排废码
				insert[i][3] = scanTime // 扫码时间
			}
		}
	}

	switch isQualified {
	case true: // 合格
		{
			// 写入成功的文件
			err := fsw.WriteAll(insert)
			fsw.Flush()
			return true, nil, err
		}
	default: // 不合格
		{
			// 写入失败的文件
			err := ffw.WriteAll(insert)
			ffw.Flush()
			return false, insert, err
		}
	}
}

// PathExists 判断路径是否存在
func PathExists(path string) bool {
	_, err := os.Stat(path)
	if err != nil {
		return false
	}
	return true
}

// DirNotExistsCreate 判断文件夹如果不存在则创建
func DirNotExistsCreate(path string) error {
	if !PathExists(path) {
		err := os.MkdirAll(path, os.ModePerm)
		if err != nil {
			return err
		}
	}
	return nil
}

// PageRange 分页范围
func PageRange(perNum int, page int) (start int, end int) {
	pageBefore := page - 1
	if pageBefore < 0 {
		pageBefore = 0
	}
	start = pageBefore * perNum
	end = page * perNum
	return
}

// Now 获取当前时间
func Now() string {
	return time.Now().Format("2006-01-02 15:04:05")
}

// QueryWasteCode 查询排废码
func QueryWasteCode(wasteCodes []string) (map[string]string, error) {
	if len(wasteCodes) == 0 {
		return nil, errors.New("")
	}
	regex := wasteCodes[0]
	wc := make(map[string]string, len(wasteCodes))
	if !CharRegexp.MatchString(regex) { // 校验是否是随机码  正常码
		for i := range wasteCodes {
			c := wasteCodes[i]
			wc[c] = c
		}
		return wc, nil
	}

	// 随机码要去后端查询
	url := Ini.JavaUrl.Base + Ini.JavaUrl.LabelCode
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		log.Println("error: ", err)
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(5)*time.Second)
	defer cancel()

	wastes, err := json.Marshal(wasteCodes)
	if err != nil {
		log.Println("error: ", err)
		return nil, err
	}

	// 设置参数
	params := req.URL.Query()
	params.Add("codesStr", string(wastes))

	req.URL.RawQuery = params.Encode()
	resp, _ := http.DefaultClient.Do(req.WithContext(ctx))
	body, _ := ioutil.ReadAll(resp.Body)

	jur := new(JavaUploadResponse)

	err = json.Unmarshal(body, jur)
	if err != nil {
		log.Println("error: ", err)
		return nil, err
	}
	if jur.Success {
		return jur.Data, nil
	}
	return nil, jur
}

// SliceInterStr 切片数字统计
func SliceInterStr(slice1, slice2 []string) []string {
	m := make(map[string]int)
	nn := make([]string, 0)
	for _, v := range slice1 {
		m[v]++
	}
	for _, v := range slice2 {
		times, _ := m[v]
		if times > 0 {
			nn = append(nn, v)
		}
	}
	return nn
}

// SliceDiffStr 切片差集
func SliceDiffStr(slice1, slice2 []string) []string {
	m := make(map[string]int)
	nn := make([]string, 0)
	inter := SliceInterStr(slice1, slice2)
	for _, v := range inter {
		m[v]++
	}
	for _, v := range slice1 {
		times, _ := m[v]
		if times == 0 {
			nn = append(nn, v)
		}
	}
	return nn
}

// SliceDuplication 切片去重
func SliceDuplication(arr []string) []string {
	set := make(map[string]struct{}, len(arr))
	j := 0
	for _, v := range arr {
		_, ok := set[v]
		if ok {
			continue
		}
		set[v] = struct{}{}
		arr[j] = v
		j++
	}

	return arr[:j]
}

// RemoveEdition 整版移除
func RemoveEdition(code string, kind int, split []string) (bool, Response) {

	var (
		orgFile string
		newFile string
		key     string
		dfs     dataframe.DataFrame
	)

	switch kind {
	case 1: // 移除上传失败记录
		{
			orgFile = filepath.Join("static", code, UpFaiFile)
			newFile = filepath.Join("static", code, "temp_"+UpFaiFile)
			key = code + GoKeyUpFaiSuffix
		}
	default: // 移除成功记录
		{
			orgFile = filepath.Join("static", code, SucFile)
			newFile = filepath.Join("static", code, "temp_"+SucFile)
			key = code + GoKeySucSuffix
		}
	}

	_ = os.Rename(orgFile, newFile)

	if dfi, ok := GoCache.Get(key); ok {
		dfs = dfi.(dataframe.DataFrame)
	} else {
		ffr, err := os.Open(newFile)
		if err != nil {
			return false, Response{
				Status: false,
				Msg:    "打开上传失败记录文件失败:" + err.Error(),
			}
		}
		fo := dataframe.HasHeader(false)
		df := dataframe.ReadCSV(ffr, fo)
		od := dataframe.RevSort("X0")
		dfs = df.Arrange(od)
		GoCache.Delete(key)
		GoCache.Set(key, dfs, GoCacheTimeout) // 设置
	}

	rows := dfs.Col("X0").Records()

	rows = SliceDiffStr(rows, split) // 计算数组差值
	rows = SliceDuplication(rows)    // 数组去重
	filter := make([]dataframe.F, 0, len(rows))

	for i := range rows {
		filter = append(filter, dataframe.F{
			Colname:    "X0",
			Comparator: series.Eq,
			Comparando: rows[i],
		})
	}

	df := dfs.Filter(filter...)

	fii, _ := os.Create(orgFile)
	defer func() {
		_ = fii.Close()
		_ = os.Remove(newFile)
	}()
	oo := dataframe.WriteHeader(false) // 不写入header
	err := df.WriteCSV(fii, oo)
	if err != nil {
		return false, Response{
			Status: false,
			Msg:    "整版移除失败",
		}
	}
	GoCache.Delete(key)
	GoCache.Set(key, df, GoCacheTimeout) // 重置缓存
	return true, Response{}
}

// FindMaxId 找到文件的最大id
func FindMaxId(fs string) (int, error) {
	var (
		e   string
		err error
	)
	fsi, _ := os.Open(fs)
	defer func() {
		_ = fsi.Close()
	}()
	if err != nil {
		e = "error:打开成功记录文件失败:" + err.Error()
		log.Println(e)
		return 0, err
	}
	fo := dataframe.HasHeader(false)
	df := dataframe.ReadCSV(fsi, fo)
	ids := df.Col("X0").Records()
	mp := make(map[string]struct{})
	for i := range ids {
		t := ids[i]
		mp[t] = struct{}{}
	}
	maxId := 0
	for k := range mp {
		i, err := strconv.Atoi(k)
		if err != nil {
			return 0, err
		}
		if i > maxId {
			maxId = i
		}
	}
	maxId += 1
	return maxId, nil
}
