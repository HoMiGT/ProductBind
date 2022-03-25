/*
@Time : 2022/3/9 2:16 下午
@Author : houmin
@File : controllers
@Software: GoLand
*/
package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-gota/gota/dataframe"
	"github.com/go-gota/gota/series"
	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"gopkg.in/resty.v1"
)

// 产品绑定 ws连接
func pdWs(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Access-Control-Allow-Origin", "*")
	defer func() {
		if err := recover(); err != nil {
			log.Println("error:", err)
		}
	}()
	header := make(http.Header, 1)
	header.Set("Content-Type", "application/json") // 设置响应格式
	conn, err := upgrader.Upgrade(w, r, header)
	if err != nil {
		log.Println("error:", "websocket 连接错误: ", err)
		return
	}

	// 退出协程信号
	quitReaderChan := make(chan struct{})
	quitWriterChan := make(chan struct{})
	quitWarnChan := make(chan struct{})

	// 读取数据的chan
	csvChan := make(chan []byte)
	// 警告信号的chan
	warnChan := make(chan struct{})

	wp := &WsParams{} // ws 的连接参数
	wc := &WsCount{}  // 统计个数

	defer func() {
		log.Println("info:", "websocket连接关闭，开始清理内存")
		// 更新记录的状态
		crr := CodeReadRecords{}
		crr.UpdateStatus(0, wp.BundleCode, "", 0)
		err := conn.WriteJSON(WsResponse{
			OperateCode: OperaArea,
			Response: Response{
				Status: true,
				Data:   QuerySignal{IsQuery: true},
			},
		})
		if err != nil {
			log.Println("error:", err)
		}

		_ = conn.Close()
		_ = wp.close()
		GoCache.Delete(GoKeyFailParse)
		quitReaderChan <- struct{}{} // 关闭读协程
		quitWriterChan <- struct{}{} // 关闭写协程
		quitWarnChan <- struct{}{}   // 关闭警告协程
		log.Println("info:", "内存清理完成")
	}()
	if wp.TipsBytes, err = ioutil.ReadFile("tips.mp3"); err != nil {
		log.Println("error:", err)
		return
	}
	if wp.WarnBytes, err = ioutil.ReadFile("warn.mp3"); err != nil {
		log.Println("error:", err)
		return
	}
	wp.SetTips(true)
	wp.SetWarn(true)

	for {
		err := conn.ReadJSON(wp)
		if err != nil {
			err = conn.WriteJSON(WsResponse{ // 向客户端发送数据
				OperateCode: OperaArea,
				Response: Response{
					Status: false,
					Msg:    err.Error(),
					Data:   nil,
				},
			})
			if err != nil {
				log.Println("error:", "websocket 发送给客户端错误: ", err)
				_ = conn.Close()
				return
			}
			err, ok := err.(*websocket.CloseError)
			if ok {
				log.Println("error:", "websocket 关闭错误: ", err)
				_ = conn.Close()
				return
			}
			continue
		}
		switch wp.OperateCode { // 操作码
		case StateStart: // 开始
			{
				log.Println("info:", "服务端接受状态: 开始")
				StartHandle(conn, csvChan, warnChan, quitReaderChan, quitWriterChan, quitWarnChan, wp, wc)
			}
		case StateStop: // 结束
			{
				log.Println("info:", "服务端接受的状态: 停止")
				StopHandle(wp)
				return
			}
		case StateContinue: // 继续
			{
				log.Println("info:", "服务端接受的状态: 继续")
				ContinueHandle(wp, wc)
			}

		case StateSuspend: // 暂停
			{
				log.Println("info:", "服务端接受的状态: 暂停")
				SuspendHandle(wp)
			}

		default: // 默认返回
			{
				log.Println("info:", "暂不支持该状态")
				if err = conn.WriteJSON(WsResponse{
					OperateCode: OperaArea,
					Response: Response{
						Status: false,
						Msg:    "暂不支持此状态码",
						Data:   nil,
					},
				}); err != nil {
					log.Println("error:", "服务端暂不支持该状态码，发送给客户端信息时错误: ", err)
				}
			}

		}
	}
}

// 展示错误的历史文件
func pdDisplay(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Access-Control-Allow-Origin", "*")
	defer func() {
		if err := recover(); err != nil {
			log.Println("error:", "退出'pdDisplay'错误:", err)
		}
	}()
	var e string
	params := r.URL.Query()
	filterCode := params.Get(Code) // 获取过滤的字段
	size := params.Get(Size)       // 获取
	page := params.Get(Page)
	if size == "" || page == "" {
		e = "error:缺少必要字段 'per_page_num' 或者 'page' "
		log.Println(e)
		_, _ = w.Write(Response{
			Status: false,
			Msg:    e,
		}.Json())
		return
	}
	sizeInt, err1 := strconv.Atoi(size)
	pageInt, err2 := strconv.Atoi(page)
	if err1 != nil || err2 != nil {
		e = "error:'per_page_num' 或者 'page' 转换整数异常 "
		log.Println(e)
		_, _ = w.Write(Response{
			Status: false,
			Msg:    e,
		}.Json())
		return
	}

	if ls, ok := GoCache.Get(GoKeyFailParse); ok { // 内存中有
		fdr := new(FailDisplayRes) // 失败展示结构体
		insert := ls.([][]string)
		length := len(insert)
		fdr.Total = length // 最大数量
		fdr.Size = sizeInt // 每页大小
		fdr.Page = pageInt // 当前页
		// 需要将错误的判断,如果非数字需要后端查询排废码
		dl := make([]Display, 0, length) // 初始化失败展示的切片
		for i := range insert {
			index := insert[i][0]
			code := insert[i][1]
			waste := insert[i][2]
			scan := insert[i][3]
			switch filterCode {
			case "", code, waste:
				{
					dl = append(dl, Display{
						Index:     index,
						Code:      code,
						ScanTime:  scan,
						WasteCode: waste,
					})
				}
			}
		}
		start, end := PageRange(sizeInt, pageInt) // 展示的范围
		l := len(dl)
		if l < end {
			end = l
		}
		fdr.Display = dl[start:end] // 展示的数据
		_, _ = w.Write(Response{    // 返回成功结果
			Status: true,
			Msg:    "",
			Data:   fdr,
		}.Json())
		return
	}
	e = "warning:未查询到数据"
	log.Println(e)
	_, _ = w.Write(Response{ // 返回失败结果
		Status: false,
		Msg:    "未查询到数据",
	}.Json())
}

// 产品绑定-历史记录
func pdHistory(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Access-Control-Allow-Origin", "*")
	defer func() {
		if err := recover(); err != nil {
			_, _ = w.Write(Response{
				Status: false,
				Msg:    err.(error).Error(),
			}.Json())
			log.Println("error:", "退出'pdHistory'错误: ", err)
		}
	}()
	query := r.URL.Query()
	size := query.Get(Size)
	page := query.Get(Page)
	code := query.Get(Code)

	sizeInt, err := strconv.Atoi(size)
	if err != nil {
		log.Println("error:", "转换size类型错误: ", err.Error())
		_, _ = w.Write(Response{
			Status: false,
			Msg:    err.Error(),
			Data:   nil,
		}.Json())
		return
	}
	pageInt, err := strconv.Atoi(page)
	if err != nil {
		log.Println("error:", "转换page类型错误: ", err.Error())
		_, _ = w.Write(Response{
			Status: false,
			Msg:    err.Error(),
			Data:   nil,
		}.Json())
		return
	}
	crr := CodeReadRecords{}
	var res []CodeReadRecords
	if code == "" {
		res = crr.Select(0, sizeInt, pageInt, "", 0)
	} else {
		res = crr.Select(0, sizeInt, pageInt, code, 2)
	}

	total := crr.Count()

	hr := new(HistoryRes)
	hr.Display = res
	hr.Size = sizeInt
	hr.Page = pageInt
	hr.Total = total
	hr.Display = res

	_, _ = w.Write(Response{
		Status: true,
		Data:   hr,
	}.Json())
}

// 产品绑定-捆码检查
func pdCheck(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Access-Control-Allow-Origin", "*")
	defer func() {
		if err := recover(); err != nil {
			log.Println("error:", err)
		}
	}()
	var e string
	pathParams := mux.Vars(r)
	if _, ok := pathParams["code"]; !ok {
		e = "error:请求的路径参数不存在"
		log.Println(e)
		_, _ = w.Write(Response{
			Status: false,
			Msg:    e,
		}.Json())
		return
	}

	url := Ini.JavaUrl.Base + Ini.JavaUrl.Check
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		e = "error: 请求后端错误: " + err.Error()
		log.Println(e)
		_, _ = w.Write(Response{
			Status: false,
			Msg:    e,
		}.Json())
		return
	}
	// 设置超时参数
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(5)*time.Second)
	defer cancel()
	// 设置参数
	params := req.URL.Query()
	params.Add("bundCode", pathParams["code"])

	req.URL.RawQuery = params.Encode()
	resp, err := http.DefaultClient.Do(req.WithContext(ctx))
	if err != nil {
		e = "error: 上传后端接口异常:" + err.Error()
		log.Println(e)
		_, _ = w.Write(Response{
			Status: false,
			Msg:    e,
		}.Json())
		return
	}
	if resp.StatusCode != 200 {
		e = "error:后端响应异常:" + resp.Status
		_, _ = w.Write(Response{
			Status: false,
			Msg:    e,
		}.Json())
		return
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		e = "error: 解析后端接口返回信息异常:" + err.Error()
		_, _ = w.Write(Response{
			Status: false,
			Msg:    e,
		}.Json())
		return
	}

	jur := new(JavaUploadResponse)
	_ = json.Unmarshal(body, &jur)
	_, _ = w.Write(Response{
		Status: jur.Success,
		Msg:    jur.ErrCode + " " + jur.ErrMsg,
		Data:   jur.Data,
	}.Json())
}

// 上传前确认
func pdUploadBefore(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Access-Control-Allow-Origin", "*")
	defer func() {
		if err := recover(); err != nil {
			log.Println("error:", err)
		}
	}()
	var e string
	params := r.URL.Query()
	code := params.Get(Code) // 获取过滤的字段
	if code == "" {
		e = "error:缺少必要字段 'code' "
		log.Println(e)
		_, _ = w.Write(Response{
			Status: false,
			Msg:    e,
		}.Json())
		return
	}

	var dfs dataframe.DataFrame
	key := code + GoKeySucSuffix
	if dfi, ok := GoCache.Get(key); ok { // 获取成功记录文件缓存
		dfs = dfi.(dataframe.DataFrame)
	} else {
		filePath := filepath.Join("static", code, SucFile) // 文件路径

		_, err := os.Stat(filePath)
		if err != nil {
			e = "error:未找到文件'" + filePath + "',请核对"
			log.Println(e)
			_, _ = w.Write(Response{
				Status: false,
				Msg:    e,
			}.Json())
			return
		}
		fsi, _ := os.Open(filePath)
		defer func() {
			_ = fsi.Close()
		}()
		if err != nil {
			e = "error:打开成功记录文件失败:" + err.Error()
			log.Println(e)
			_, _ = w.Write(Response{
				Status: false,
				Msg:    e,
			}.Json())
			return
		}
		fo := dataframe.HasHeader(false)
		df := dataframe.ReadCSV(fsi, fo)
		order := dataframe.RevSort("X0")
		dfs = df.Arrange(order)
		GoCache.Delete(key)
		GoCache.Set(key, dfs, GoCacheTimeout) // 设置成功记录文件缓存
	}

	ids := dfs.Col("X0").Records()
	mp := make(map[string]struct{})
	for i := range ids {
		t := ids[i]
		mp[t] = struct{}{}
	}
	index := len(mp)
	total := dfs.Nrow()
	_, _ = w.Write(Response{
		Status: true,
		Data: FileStatistic{
			IndexCount: index,
			LabelCount: total,
		}}.Json())

}

// 产品绑定-上传记录
func pdUpload(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Access-Control-Allow-Origin", "*")
	defer func() {
		if err := recover(); err != nil {
			log.Println("error:", err)
		}
	}()
	var e string
	pathParams := mux.Vars(r) // 解析路径参数
	id, ok := pathParams["id"]
	if !ok {
		e = "error: 请求的路径参数不存在"
		log.Println(e)
		_, _ = w.Write(Response{
			Status: false,
			Msg:    e,
		}.Json())
		return
	}
	idInt, err := strconv.Atoi(id)
	if err != nil {
		e = "error:转换路径参数类型失败，请核对"
		log.Println(e)
		_, _ = w.Write(Response{
			Status: false,
			Msg:    e,
		}.Json())
		return
	}

	code, ok := pathParams["code"]
	if !ok {
		e = "error:请求的路径参数不存在"
		log.Println(e)
		_, _ = w.Write(Response{
			Status: false,
			Msg:    e,
		}.Json())
		return
	}
	filePath := filepath.Join("static", code, SucFile) // 文件路径

	_, err = os.Stat(filePath)
	if err != nil {
		e = "error:未找到文件'" + filePath + "',请核对"
		log.Println(e)
		_, _ = w.Write(Response{
			Status: false,
			Msg:    e,
		}.Json())
		return
	}

	file, _ := os.Open(filePath)
	defer func() {
		_ = file.Close()
	}()

	crr := CodeReadRecords{}
	rest := resty.R().SetFileReader("file", filepath.Base(code+".csv"), file)
	url := Ini.JavaUrl.Base + Ini.JavaUrl.Upload
	resp, err := rest.Post(url)
	if err != nil {
		crr.UpdateStatus(idInt, "", err.Error(), 3)
		e = "error:上传文件失败:" + err.Error()
		_, _ = w.Write(Response{
			Status: false,
			Msg:    e,
		}.Json())
		return
	}

	jur := new(JavaUploadResponse)
	err = json.Unmarshal(resp.Body(), jur)
	if err != nil {
		crr.UpdateStatus(idInt, "", jur.ErrMsg, 3)
		e = "error:解析后端接口成结构体异常:" + err.Error()
		_, _ = w.Write(Response{
			Status: false,
			Msg:    e,
		}.Json())
	}

	if jur.Success { // 更新成功
		crr.UpdateStatus(idInt, "", "", 2) // 成功 更新成功id
		e = "info:上传成功"
		log.Println(e)
		_, _ = w.Write(Response{
			Status: true,
			Msg:    e,
		}.Json())
		return
	} else { // 更新失败
		// 失败 将返回的内容写入新的文件内
		crr.UpdateStatus(idInt, "", jur.ErrMsg, 3)
		ud := filepath.Join("static", code)
		err = DirNotExistsCreate(ud)
		if err != nil {
			e = "error:创建写入上传失败记录的文件夹失败:" + err.Error()
			log.Println(err)
			_, _ = w.Write(Response{
				Status: false,
				Msg:    e,
			}.Json())
			return
		}
		uf := filepath.Join(ud, UpFaiFile)
		_ = os.RemoveAll(uf)
		ufh, err := os.Create(uf) // 创建写入失败的文件句柄
		if err != nil {
			e = "error:创建写入上传失败记录的文件失败:" + err.Error()
			log.Println(err)
			_, _ = w.Write(Response{
				Status: false,
				Msg:    e,
			}.Json())
			return
		}
		defer func() {
			_ = ufh.Close()
		}()

		ufw := csv.NewWriter(ufh) // 创建成功csv的writer对象

		fl := make([]dataframe.F, 0, len(jur.Data))
		for k := range jur.Data {
			fl = append(fl, dataframe.F{
				Colname:    "X1",
				Comparator: series.Eq,
				Comparando: k,
			})
		}

		var dfs dataframe.DataFrame
		key := code + GoKeySucSuffix
		if dfi, ok := GoCache.Get(key); ok {
			dfs = dfi.(dataframe.DataFrame)
		} else {
			fs := filepath.Join(ud, SucFile)
			fsi, err := os.Open(fs)
			if err != nil {
				e = "error:打开成功记录的文件失败:" + err.Error()
				log.Println(e)
				_, _ = w.Write(Response{
					Status: false,
					Msg:    e,
				}.Json())
				return
			}
			defer func() {
				_ = fsi.Close()
			}()

			op := dataframe.HasHeader(false)
			dfs = dataframe.ReadCSV(fsi, op)
		}

		if len(fl) != 0 {
			df := dfs.Filter(fl...) // 过滤出上传失败的df
			rs := df.Records()
			rs = rs[1:]
			records := make([][]string, len(rs))
			for i := range records {
				records[i] = make([]string, 4, 4)
			}
			for i := range rs {
				c := rs[i][1]
				records[i][0] = rs[i][0] // 版索引
				records[i][1] = c        // 数码
				w, ok := jur.Data[c]
				if !ok {
					w = c
				}
				records[i][2] = w        // 排废码
				records[i][3] = rs[i][2] // 扫码时间
			}

			err = ufw.WriteAll(records)
			if err != nil {
				e = "error:写入上传文件失败，失败信息:" + err.Error()
				log.Println(e)
				_, _ = w.Write(Response{
					Status: false,
					Msg:    e,
				}.Json())
			}
			od := dataframe.RevSort("X0")
			df = df.Arrange(od)
		}
		e = "warning:上传文件失败,后端返回错误:" + jur.ErrMsg
		log.Println(e)
		_, _ = w.Write(Response{
			Status: false,
			Msg:    e,
		}.Json())
	}
}

// 产品绑定-查看记录
func pdLookup(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Access-Control-Allow-Origin", "*")
	defer func() {
		if err := recover(); err != nil {
			log.Println("error:", err)
		}
	}()
	var e string
	pathParams := mux.Vars(r)
	id, ok := pathParams[ID] // 获取路径参数id
	if !ok {
		e = "error:请求的路径参数不存在"
		log.Println(e)
		_, _ = w.Write(Response{
			Status: false,
			Msg:    e,
		}.Json())
		return
	}
	idInt, err := strconv.Atoi(id)
	if err != nil {
		e = "error:转换路径参数类型失败，请核对"
		log.Println(e)
		_, _ = w.Write(Response{
			Status: false,
			Msg:    e,
		}.Json())
		return
	}
	code, ok := pathParams[Code] // 获取路径参数code
	if !ok {
		e = "error:请求的路径参数不存在"
		log.Println(e)
		_, _ = w.Write(Response{
			Status: false,
			Msg:    e,
		}.Json())
		return
	}
	query := r.URL.Query()
	size := query.Get(Size) // 获取请求参数per_page_num
	sizeInt, err := strconv.Atoi(size)
	if err != nil {
		e = "error:转换路径参数类型失败，请核对"
		log.Println(e)
		_, _ = w.Write(Response{
			Status: false,
			Msg:    e,
		}.Json())
		return
	}
	page := query.Get(Page) // 获取请求参数 page
	pageInt, err := strconv.Atoi(page)
	if err != nil {
		e = "error:转换路径参数类型失败，请核对"
		log.Println(e)
		_, _ = w.Write(Response{
			Status: false,
			Msg:    e,
		}.Json())
		return
	}
	filterCode := query.Get(Code) // 获取请求参数 过滤的code
	kind := query.Get(Kind)       // 获取请求参数类型 0-表示查询所有 1-表示查询成功 2-表示查询失败
	kindInt, err := strconv.Atoi(kind)
	if err != nil {
		e = "error:转换路径参数类型失败，请核对"
		log.Println(e)
		_, _ = w.Write(Response{
			Status: false,
			Msg:    e,
		}.Json())
		return
	}
	start, end := PageRange(sizeInt, pageInt)

	fs := filepath.Join("static", code, SucFile) // 成功文件
	ff := filepath.Join("static", code, FaiFile) // 上传失败文件

	var (
		sc int       // 成功统计个数
		fc int       // 失败统计个数
		sl []Display // 成功数据
		fl []Display // 失败数据

		dfs dataframe.DataFrame
		dff dataframe.DataFrame
	)
	if kindInt == 0 || kindInt == 1 { // 全部或者成功
		key := code + GoKeySucSuffix
		if dfi, ok := GoCache.Get(key); ok { // 获取成功记录文件缓存
			dfs = dfi.(dataframe.DataFrame)
		} else {
			fsi, err := os.Open(fs)
			if err != nil {
				e = "error:打开成功记录文件失败:" + err.Error()
				log.Println(e)
				_, _ = w.Write(Response{
					Status: false,
					Msg:    e,
				}.Json())
				return
			}
			defer func() {
				_ = fsi.Close()
			}()
			fo := dataframe.HasHeader(false)
			df := dataframe.ReadCSV(fsi, fo)
			order := dataframe.RevSort("X0")
			dfs = df.Arrange(order)
			GoCache.Delete(key)
			GoCache.Set(key, dfs, GoCacheTimeout) // 设置成功记录文件缓存
		}
		sc = dfs.Nrow()
		if filterCode != "" {
			dfs = dfs.Filter(dataframe.F{
				Colname:    "X1",
				Comparator: series.Eq,
				Comparando: filterCode,
			})
			sc = dfs.Nrow()
		}
		rc := dfs.Records()
		rcv := rc[1:]
		if end > len(rcv) {
			end = len(rcv)
		}
		rcv = rcv[start:end]
		for i := range rcv {
			it := rcv[i]
			sl = append(sl, Display{
				Index:    it[0],
				Code:     it[1],
				ScanTime: it[2],
			})
		}
	}

	if kindInt == 0 || kindInt == 2 { // 全部或者失败
		key := code + GoKeyFaiSuffix
		if dfi, ok := GoCache.Get(key); ok { // 获取失败记录文件缓存
			dff = dfi.(dataframe.DataFrame)
		} else {
			ffi, err := os.Open(ff)
			if err != nil {
				e = "error:打开失败记录文件失败:" + err.Error()
				_, _ = w.Write(Response{
					Status: false,
					Msg:    e,
				}.Json())
				return
			}
			defer func() {
				_ = ffi.Close()
			}()
			fo := dataframe.HasHeader(false)
			df := dataframe.ReadCSV(ffi, fo)
			order := dataframe.RevSort("X0")
			dff = df.Arrange(order)
			GoCache.Delete(key)
			GoCache.Set(key, dff, GoCacheTimeout) // 设置失败记录文件缓存
		}
		fc = dff.Nrow()
		if filterCode != "" {
			dff = dff.Filter(dataframe.F{
				Colname:    "X1",
				Comparator: series.Eq,
				Comparando: filterCode,
			})
			fc = dff.Nrow()
		}
		rc := dff.Records()
		rcv := rc[1:]
		if end > len(rcv) {
			end = len(rcv)
		}
		rcv = rcv[start:end]
		for i := range rcv {
			it := rcv[i]
			fl = append(fl, Display{
				Index:     it[0],
				Code:      it[1],
				ScanTime:  it[2],
				WasteCode: it[3],
			})
		}
	}
	crr := CodeReadRecords{}
	record := crr.Select(idInt, 0, 0, "", 1)

	lur := new(LookUpRes)
	lur.Page = pageInt
	lur.Size = sizeInt
	if len(record) > 0 {
		lur.Record = record[0]
	} else {
		lur.Record = crr
	}
	lur.Success = sl
	lur.Fail = fl
	lur.STotal = sc
	lur.FTotal = fc

	_, _ = w.Write(Response{
		Status: true,
		Msg:    "",
		Data:   lur,
	}.Json())

}

// 产品绑定-查看失败原因
func pdFail(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Access-Control-Allow-Origin", "*")
	defer func() {
		if err := recover(); err != nil {
			log.Println("error:", err)
		}
	}()
	var e string
	pathParams := mux.Vars(r)
	code, ok := pathParams[Code] // 获取路径参数code
	if !ok {
		e = "error:请求的路径参数不存在"
		log.Println(e)
		_, _ = w.Write(Response{
			Status: false,
			Msg:    e,
		}.Json())
		return
	}
	query := r.URL.Query()
	size := query.Get(Size) // 获取请求参数per_page_num
	sizeInt, err := strconv.Atoi(size)
	if err != nil {
		e = "error:转换路径参数类型失败，请核对"
		log.Println(e)
		_, _ = w.Write(Response{
			Status: false,
			Msg:    e,
		}.Json())
		return
	}
	page := query.Get(Page) // 获取请求参数 page
	pageInt, err := strconv.Atoi(page)
	if err != nil {
		e = "error:转换路径参数类型失败，请核对"
		log.Println(e)
		_, _ = w.Write(Response{
			Status: false,
			Msg:    e,
		}.Json())
		return
	}
	res := new(FailUploadRes)
	res.Size = sizeInt
	res.Page = pageInt

	var dff dataframe.DataFrame
	key := code + GoKeyUpFaiSuffix
	if dfi, ok := GoCache.Get(key); ok { // 获取上传失败文件缓存
		dff = dfi.(dataframe.DataFrame)
	} else {
		ff := filepath.Join("static", code, UpFaiFile)
		ffi, err := os.Open(ff)
		if err != nil {
			res.Total = 0
			e = "error:打开上传失败记录文件失败:" + err.Error()
			log.Println(e)
			_, _ = w.Write(Response{
				Status: false,
				Msg:    e,
				Data:   res,
			}.Json())
			return
		}
		defer func() {
			_ = ffi.Close()
		}()
		fo := dataframe.HasHeader(false)
		df := dataframe.ReadCSV(ffi, fo)
		od := dataframe.RevSort("X0")
		dff = df.Arrange(od)
		GoCache.Delete(key)
		GoCache.Set(key, dff, GoCacheTimeout) // 设置上传文件失败文件缓存
	}

	total := dff.Nrow()
	res.Total = total
	rd := dff.Records()
	rd = rd[1:]
	start, end := PageRange(sizeInt, pageInt)
	rd = rd[start:end]

	var fl []Display
	for i := range rd {
		it := rd[i]
		fl = append(fl, Display{
			Index:     it[0],
			Code:      it[1],
			WasteCode: it[2],
			ScanTime:  it[3],
		})
	}
	res.Display = fl
	_, _ = w.Write(Response{
		Status: true,
		Msg:    "",
		Data:   res,
	}.Json())

}

// 失败原因详情
func pdFailRecord(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Access-Control-Allow-Origin", "*")
	defer func() {
		if err := recover(); err != nil {
			log.Println("error:", err)
		}
	}()
	var e string
	pathParams := mux.Vars(r)
	id, ok := pathParams[ID] // 获取路径参数id
	if !ok {
		e = "error:请求的路径参数不存在"
		log.Println(e)
		_, _ = w.Write(Response{
			Status: false,
			Msg:    e,
		}.Json())
		return
	}
	idInt, err := strconv.Atoi(id)
	if err != nil {
		e = "error:转换路径参数类型失败，请核对"
		log.Println(e)
		_, _ = w.Write(Response{
			Status: false,
			Msg:    e,
		}.Json())
		return
	}
	crr := CodeReadRecords{}
	re := crr.Select(idInt, 0, 0, "", 1)

	var record CodeReadRecords
	if len(re) > 0 {
		record = re[0]
	} else {
		record = crr
	}
	_, _ = w.Write(Response{
		Status: true,
		Data:   record,
	}.Json())
}

// 产品绑定-整版移除搜索
func pdFilter(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Access-Control-Allow-Origin", "*")
	defer func() {
		if err := recover(); err != nil {
			log.Println("error:", err)
		}
	}()
	var e string
	pathParams := mux.Vars(r)
	code, ok := pathParams[Code] // 获取路径参数code
	if !ok {
		e = "error:请求的路径参数不存在"
		log.Println(e)
		_, _ = w.Write(Response{
			Status: false,
			Msg:    e,
		}.Json())
		return
	}
	query := r.URL.Query()
	filCode := query.Get(Code)

	var dff dataframe.DataFrame
	key := code + GoKeyUpFaiSuffix
	if dfi, ok := GoCache.Get(key); ok { // 获取上传失败文件缓存
		dff = dfi.(dataframe.DataFrame)
	} else {
		ff := filepath.Join("static", code, UpFaiFile)
		ffi, err := os.Open(ff)
		if err != nil {
			e = "error:打开上传失败记录文件失败:" + err.Error()
			log.Println(e)
			_, _ = w.Write(Response{
				Status: false,
				Msg:    e,
			}.Json())
			return
		}
		defer func() {
			_ = ffi.Close()
		}()
		fo := dataframe.HasHeader(false)
		df := dataframe.ReadCSV(ffi, fo)
		od := dataframe.RevSort("X0")
		dff = df.Arrange(od)
		GoCache.Delete(key)
		GoCache.Set(key, dff, GoCacheTimeout) // 设置上传失败文件缓存
	}
	df := dff.Filter(
		dataframe.F{
			Colname:    "X1",
			Comparator: series.Eq,
			Comparando: filCode,
		},
		dataframe.F{
			Colname:    "X2",
			Comparator: series.Eq,
			Comparando: filCode,
		},
	)

	fil := df.Records()
	fil = fil[1:]
	dl := make([]Display, 0, len(fil))
	for i := range fil {
		it := fil[i]
		dl = append(dl, Display{
			Index:     it[0],
			Code:      it[1],
			WasteCode: it[2],
			ScanTime:  it[3],
		})
	}

	_, _ = w.Write(Response{
		Status: true,
		Data:   dl,
	}.Json())

}

// 产线绑定-整版移除
func pdRemove(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Access-Control-Allow-Origin", "*")
	defer func() {
		if err := recover(); err != nil {
			log.Println("error:", err)
		}
	}()
	var e string
	pathParams := mux.Vars(r)
	code, ok := pathParams[Code] // 获取路径参数code
	if !ok {
		e = "error:请求的路径参数不存在"
		log.Println(e)
		_, _ = w.Write(Response{
			Status: false,
			Msg:    e,
		}.Json())
		return
	}
	query := r.URL.Query()
	index := query.Get(Index)
	if index == "" {
		e = "error:请输入正确的版索引"
		log.Println(e)
		_, _ = w.Write(Response{Status: false, Msg: e}.Json())
		return
	}
	indexSplit := strings.Split(index, ",")

	f1, rs := RemoveEdition(code, 0, indexSplit) // 移除成功记录的版数
	if !f1 {
		_, _ = w.Write(rs.Json())
		return
	}
	f2, rs := RemoveEdition(code, 1, indexSplit) // 移除上传失败记录的版数
	if !f2 {
		_, _ = w.Write(rs.Json())
		return
	}
	_, _ = w.Write(Response{
		Status: true,
		Msg:    "整版移除成功",
	}.Json())

}

// 产线绑定-删除记录
func pdDelete(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Access-Control-Allow-Origin", "*")
	defer func() {
		if err := recover(); err != nil {
			log.Println("error:", err)
		}
	}()
	var e string
	pathParams := mux.Vars(r)
	id, ok := pathParams[ID] // 获取路径参数id
	if !ok {
		e = "error:请求的路径参数不存在"
		log.Println(e)
		_, _ = w.Write(Response{
			Status: false,
			Msg:    e,
		}.Json())
		return
	}
	idInt, err := strconv.Atoi(id)
	if err != nil {
		e = "error:转换路径参数类型失败，请核对"
		log.Println(e)
		_, _ = w.Write(Response{
			Status: false,
			Msg:    e,
		}.Json())
		return
	}
	code, ok := pathParams[Code] // 获取路径参数code
	if !ok {
		e = "error:请求的路径参数不存在"
		log.Println(e)
		_, _ = w.Write(Response{
			Status: false,
			Msg:    e,
		}.Json())
		return
	}

	s := filepath.Join("static", code, SucFile)
	f := filepath.Join("static", code, FaiFile)
	u := filepath.Join("static", code, UpFaiFile)
	d := filepath.Join("static", code)

	_ = os.Remove(s)
	_ = os.Remove(f)
	_ = os.Remove(u)
	_ = os.RemoveAll(d)

	crr := CodeReadRecords{}
	c := crr.Delete(idInt)
	if c > 0 {
		key := code + GoKeySucSuffix
		GoCache.Delete(key) // 清空成功文件缓存
		key = code + GoKeyFaiSuffix
		GoCache.Delete(key) // 清空失败文件缓存
		key = code + GoKeyUpFaiSuffix
		GoCache.Delete(key) // 清空上传失败文件缓存
		_, _ = w.Write(Response{
			Status: true,
			Msg:    "删除记录成功",
		}.Json())
	} else {
		e = "warning:删除记录失败"
		log.Println(e)
		_, _ = w.Write(Response{
			Status: false,
			Msg:    e,
		}.Json())
	}

}
