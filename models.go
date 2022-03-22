/*
@Time : 2022/3/9 2:15 下午
@Author : houmin
@File : models
@Software: GoLand
*/
package main

import (
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"log"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/hajimehoshi/oto"
	_ "github.com/mattn/go-sqlite3"
	"github.com/patrickmn/go-cache"
)

var (
	OperaArea = 1001 // 操作区域

	StateStart    = 101 // 开始
	StateStop     = 102 // 结束
	StateSuspend  = 103 // 暂停
	StateContinue = 104 // 继续
)

// 读码状态
const (
	StateReading       = iota // 读码中  0
	StateUploading            // 上传中  1
	StateUploadFail           // 上传失败 2
	StateNoUpload             // 未上传  3
	StateUploadSuccess        // 上传成功  4

)

// 保存文件常量
const (
	SucFile   = "success.csv" // 成功文件名
	FaiFile   = "fail.csv"    // 失败文件名
	UpFaiFile = "upFail.csv"  // 上传失败文件名
)

// QuerySignal 传送查询信号
type QuerySignal struct {
	IsQuery bool `json:"is_query"` // 是否查询
}

// 请求响应中的参数名
const (
	ID    = "id"
	Code  = "code"
	Size  = "size"
	Page  = "page"
	Kind  = "kind"
	Index = "index"
)

// Ini 全局配置
var Ini *Config

var Ctx *oto.Context

var (
	// CharRegexp 数据校验正则
	CharRegexp = regexp.MustCompile(`[a-z]+`)
	// GoCache 本地缓存
	GoCache *cache.Cache
	// GoKeyFailParse 解析失败缓存
	GoKeyFailParse = "GoKeyFailParse"
	// GoKeySucSuffix 成功记录缓存后缀
	GoKeySucSuffix = "_SS"
	// GoKeyFaiSuffix 失败记录缓存后缀
	GoKeyFaiSuffix = "_FS"
	// GoKeyUpFaiSuffix 上传失败记录缓存后缀
	GoKeyUpFaiSuffix = "_UFS"
	// GoCacheTimeout 缓存超时时间
	GoCacheTimeout = time.Minute * 30
)

// Display 失败展示的结构体
type Display struct {
	Index     string `json:"index"`      // 版索引
	Code      string `json:"code"`       // 编码
	ScanTime  string `json:"scan_time"`  // 扫码时间
	WasteCode string `json:"waste_code"` // 箱码
}

// PageSplit 分页
type PageSplit struct {
	Total int `json:"total"` // 总计个数
	Page  int `json:"page"`  // 当前页
	Size  int `json:"size"`  // 每页大小
}

// FailDisplayRes 响应结构体
type FailDisplayRes struct {
	Display []Display `json:"display"` // 展示数据
	PageSplit
}

// FailUploadRes 上传失败响应结构体
type FailUploadRes struct {
	Display []Display `json:"display"` //  展示数据
	PageSplit
}

// HistoryRes 历史记录响应结构体
type HistoryRes struct {
	Display []CodeReadRecords `json:"display"` // 展示数据
	PageSplit
}

// LookUpRes 查看返回体
type LookUpRes struct {
	Record  CodeReadRecords `json:"record"`  // 记录
	Success []Display       `json:"success"` // 成功显示
	Fail    []Display       `json:"fail"`    // 错误显示
	STotal  int             `json:"stotal"`  // 成功总数
	FTotal  int             `json:"ftotal"`  // 失败总数
	Page    int             `json:"page"`    // 当前页
	Size    int             `json:"size"`    // 每页大小
}

// WsParams 请求参数结构体
type WsParams struct {
	OperateCode int    `json:"operate_code"` // 操作编码
	BundleCode  string `json:"bundle_code"`  // 捆码
	Limit       int    `json:"limit"`        // 限制个数
	IsIntoBox   int    `json:"is_into_box"`  // 是否装箱

	close  func() error // 关闭客户端函数
	lock   sync.RWMutex // 读写锁
	keep   bool         // 是否继续执行  默认false  当状态是 StateStart StateContinue 是该值为True
	isStop bool         // 是否停止

	isTips    bool   // 当前播放是否是提醒的decode
	isWarn    bool   // 当前播放是否是警告的decode
	TipsBytes []byte // tips.mp3 文件的字节数
	WarnBytes []byte // warn.mp3 文件的字节数
}

// IsWarn 获取isWarn的值
func (w *WsParams) IsWarn() bool {
	w.lock.RLock()
	defer w.lock.RUnlock()
	return w.isWarn
}

// SetWarn 设置isWarn
func (w *WsParams) SetWarn(b bool) {
	w.lock.Lock()
	defer w.lock.Unlock()
	w.isWarn = b
}

// IsTips 播放类型是否是提醒
func (w *WsParams) IsTips() bool {
	w.lock.RLock()
	w.lock.RUnlock()
	return w.isTips
}

// SetTips 设置播放类型是提醒
func (w *WsParams) SetTips(b bool) {
	w.lock.Lock()
	w.lock.Unlock()
	w.isTips = b
}

// Stop 停止状态
func (w *WsParams) Stop() bool {
	w.lock.RLock()
	defer w.lock.RUnlock()
	return w.isStop
}

// SetStop 设置停止状态
func (w *WsParams) SetStop() {
	w.lock.Lock()
	defer w.lock.Unlock()
	w.isStop = true
}

// SetKeep 设置继续状态
func (w *WsParams) SetKeep(k bool) {
	w.lock.Lock()
	defer w.lock.Unlock()
	w.keep = k
}

// GetKeep 获得继续状态
func (w *WsParams) GetKeep() bool {
	w.lock.RLock()
	defer w.lock.RUnlock()
	return w.keep
}

// WsResponse 失败返回结构体
type WsResponse struct {
	OperateCode int `json:"operate_code"`
	Response
}

// WsCount 统计个数
type WsCount struct {
	lock sync.RWMutex // 修改锁
	Sum  int          `json:"sum_count"`     // 总计个数
	Suc  int          `json:"success_count"` // 成功个数
	Fail int          `json:"fail_count"`    // 失败个数
}

// SetValue 加锁设置值
func (w *WsCount) SetValue() {
	w.lock.Lock()
	defer w.lock.Unlock()
	w.Sum += 1
	w.Suc += 1
}

// SetValueFail 加锁设置失败情况的值
func (w *WsCount) SetValueFail() {
	w.lock.Lock()
	defer w.lock.Unlock()
	w.Sum += 1
	w.Fail += 1
}

// ReSetValue 加锁重置失败后的值
func (w *WsCount) ReSetValue() {
	w.lock.Lock()
	defer w.lock.Unlock()
	w.Sum -= w.Fail
	w.Fail = 0
}

// GetValue 加锁获取值
func (w *WsCount) GetValue() WsCount {
	w.lock.RLock()
	defer w.lock.RUnlock()
	sum := w.Sum
	suc := w.Suc
	fail := w.Fail
	return WsCount{
		Sum:  sum,
		Suc:  suc,
		Fail: fail,
	}
}

// Json 将结构体转换成json
func (w WsResponse) Json() []byte {
	m, err := json.Marshal(w)
	if err != nil {
		log.Println("error:转化为json格式错误,", err)
		return nil
	}
	return m
}

// Response 统一返回响应结构体
type Response struct {
	Status bool        `json:"status"`
	Msg    string      `json:"msg"`
	Data   interface{} `json:"data"`
}

// Json 响应结构体的字节化
func (r Response) Json() []byte {
	m, err := json.Marshal(r)
	if err != nil {
		log.Println("error:转化为json格式错误,", err)
		return nil
	}
	return m
}

// NullInt 自定义类型 兼容null
type NullInt int

func (n *NullInt) Scan(value interface{}) error {
	if value == nil {
		*n = 0
		return nil
	}
	var val string
	switch v := value.(type) {
	case string:
		val = v
	case []byte:
		val = string(v)
	case int64:
		*n = NullInt(v)
		return nil
	}
	if val == "" {
		*n = 0
		return nil
	}
	i, err := strconv.ParseInt(val, 10, 64)
	if err != nil {
		return err
	}
	*n = NullInt(i)
	return nil
}

func (n NullInt) Value() (driver.Value, error) {
	if n == 0 {
		return nil, nil
	}
	return int(n), nil
}

// NullString 自定义类型 兼容null
type NullString string

func (n *NullString) Scan(value interface{}) error {
	if value == nil {
		*n = ""
		return nil
	}
	var val string
	switch v := value.(type) {
	case string:
		val = v
	case []byte:
		val = string(v)
	}
	*n = NullString(val)
	return nil
}

func (n NullString) Value() (driver.Value, error) {
	if n == "" {
		return nil, nil
	}
	return string(n), nil
}

// CodeReadRecords 读码记录结构体
type CodeReadRecords struct {
	ID           NullInt    `db:"id" json:"id"`                         // id
	BundleCode   NullString `db:"bundle_code" json:"bundle_code"`       // 捆码
	IsIntoBox    NullInt    `db:"is_into_box" json:"is_into_box"`       // 是否装箱
	ScanCodeTime NullString `db:"scan_code_time" json:"scan_code_time"` // 扫码时间
	Status       NullInt    `db:"status" json:"status"`                 // 状态
	UploadTime   NullString `db:"upload_time" json:"upload_time"`       // 上传时间
	FailReason   NullString `db:"fail_reason" json:"fail_reason"`       // 失败原因
}

// Insert 插入数据
func (c CodeReadRecords) Insert(bundleCode string, isIntoBox int, scanCodeTime string) int {
	db, err := sql.Open("sqlite3", "records.sqlite") // 打开数据库
	if err != nil {
		panic(err)
	}
	defer db.Close()
	stmt, err := db.Prepare("INSERT INTO code_read_records(bundle_code,is_into_box,scan_code_time,status) VALUES (?,?,?,?)") // 插入的sql
	if err != nil {
		panic(err)
	}
	res, err := stmt.Exec(bundleCode, isIntoBox, scanCodeTime, StateReading) // 执行sql
	if err != nil {
		panic(err)
	}
	id, err := res.LastInsertId() // 获取插入的最新id
	if err != nil {
		panic(err)
	}
	return int(id)
}

// Delete 删除记录
func (c CodeReadRecords) Delete(id int) int {
	db, err := sql.Open("sqlite3", "records.sqlite") // 打开数据库
	if err != nil {
		panic(err)
	}
	defer db.Close()
	stmt, err := db.Prepare("DELETE FROM code_read_records WHERE id = ?") // sql语句
	if err != nil {
		panic(err)
	}
	res, err := stmt.Exec(id) // 执行sql
	if err != nil {
		panic(err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		panic(err)
	}
	return int(affected)
}

// UpdateStatus 更新
func (c CodeReadRecords) UpdateStatus(id int, code string, reason string, kind int) int {
	db, err := sql.Open("sqlite3", "records.sqlite") // 打开数据库
	if err != nil {
		panic(err)
	}
	defer db.Close()
	var (
		rowsAffected int64
		res          sql.Result
	)
	current := Now()
	switch kind {
	case 0: // 更新读码完成  需要传入的参数 code
		{
			stmt, err := db.Prepare("UPDATE code_read_records SET status = ? where bundle_code = ?") // 更新读码完成状态
			if err != nil {
				panic(err)
			}
			res, err = stmt.Exec(StateNoUpload, code)
			if err != nil {
				panic(err)
			}
		}
	case 1: // 更新正在上传的任务  需要传入的参数 id
		{
			stmt, err := db.Prepare("UPDATE code_read_records SET status = ? WHERE id = ?")
			if err != nil {
				panic(err)
			}
			res, err = stmt.Exec(StateUploading, id)
			if err != nil {
				panic(err)
			}
		}
	case 2: // 更新上传成功  需要传入的参数 id
		{
			stmt, err := db.Prepare("UPDATE code_read_records SET status = ?,upload_time = ? WHERE id = ?")
			if err != nil {
				panic(err)
			}
			res, err = stmt.Exec(StateUploadSuccess, current, id)
			if err != nil {
				panic(err)
			}
		}
	case 3: // 更新上传失败  需要传入的参数  reason id
		{
			stmt, err := db.Prepare("UPDATE code_read_records SET status = ?,upload_time = ?,fail_reason = ? WHERE id = ?")
			if err != nil {
				panic(err)
			}
			res, err = stmt.Exec(StateUploadFail, current, reason, id)
			if err != nil {
				panic(err)
			}
		}
	default:
		panic("不支持该类型")
	}
	rowsAffected, err = res.RowsAffected() // 返回影响行数的个数
	if err != nil {
		return 0
	}
	return int(rowsAffected)
}

func (c CodeReadRecords) Count() int {
	db, err := sql.Open("sqlite3", "records.sqlite") // 打开数据库
	if err != nil {
		panic(err)
	}
	defer db.Close()

	sqlStr := "select count(*) count from code_read_records"
	rows, err := db.Query(sqlStr)
	if err != nil {
		log.Println(err)
		return 0
	}
	count := new(int)
	for rows.Next() {
		err = rows.Scan(count)
		if err != nil {
			log.Println(err)
			continue
		}
	}
	return *count
}

// Select 查询
func (c CodeReadRecords) Select(id int, perNum int, page int, code string, kind int) []CodeReadRecords {
	db, err := sql.Open("sqlite3", "records.sqlite") // 打开数据库
	if err != nil {
		panic(err)
	}
	defer db.Close()

	start, _ := PageRange(perNum, page)

	var sqlStr string
	switch kind {
	case 0: // 查询全部  需要参数 perNum page kind
		sqlStr = "SELECT * FROM code_read_records ORDER BY status ASC,scan_code_time DESC LIMIT " + strconv.Itoa(perNum) + " OFFSET " + strconv.Itoa(start)
	case 1: // 根据id查询  需要参数 id kind
		sqlStr = "SELECT * FROM code_read_records WHERE id = " + strconv.Itoa(id)
	case 2:
		sqlStr = "SELECT * FROM code_read_records WHERE bundle_code = '" + code + "' " + "ORDER BY scan_code_time DESC LIMIT " + strconv.Itoa(perNum) + " OFFSET " + strconv.Itoa(start)
	default:
		panic("不支持该类型")
	}

	rows, err := db.Query(sqlStr)
	if err != nil {
		panic(err)
	}
	defer rows.Close()

	res := make([]CodeReadRecords, 0, 10)

	for rows.Next() {
		c := CodeReadRecords{}
		err = rows.Scan(&c.ID, &c.BundleCode, &c.IsIntoBox, &c.ScanCodeTime, &c.Status, &c.UploadTime, &c.FailReason)
		if err != nil {
			log.Println(err)
			continue
		}
		res = append(res, c)
	}
	return res
}

// Rule 判断规则
type Rule struct {
	IndexPosition []int  `json:"IndexPosition" toml:"IndexPosition"` // 索引
	StartWith     string `json:"StartWith" toml:"StartWith"`         // 开始匹配
	EndWith       string `json:"EndWith" toml:"EndWith"`             // 结束匹配
	Contain       string `json:"Contain" toml:"Contain"`             // 包含
}

// Rules 配置的多个规则
type Rules struct {
	Box   Rule `json:"Box" toml:"Box"`
	Label Rule `json:"Label" toml:"Label"`
}

// ReadRules 读取配置的规则
func ReadRules() *Rules {
	r := new(Rules)
	if _, err := toml.DecodeFile("rule.toml", r); err != nil {
		panic(err)
	}
	return r
}

// Service 服务器启动地址
type Service struct {
	Ip   string `json:"Ip" toml:"Ip"`     // ip地址
	Port int    `json:"Port" toml:"Port"` // 端口号
}

// Camera 摄像头连接地址
type Camera struct {
	Service
}

// JavaUrl 请求后端接口
type JavaUrl struct {
	Base      string `json:"Base" toml:"Base"`
	Check     string `json:"Check" toml:"Check"`
	LabelCode string `json:"Check" toml:"LabelCode"`
	Upload    string `json:"Upload" toml:"Upload"`
}

// Config 程序启动的配置
type Config struct {
	Service `json:"Service" toml:"Service"`
	Camera  `json:"Camera" toml:"Camera"`
	JavaUrl `json:"JavaUrl" toml:"JavaUrl"`
}

func ReadConfig() *Config {
	r := new(Config)
	if _, err := toml.DecodeFile("config.toml", r); err != nil {
		panic(err)
	}
	return r
}

// JavaUploadResponse 后端上传文件响应结构体
type JavaUploadResponse struct {
	Success bool              `json:"success"` // 请求状态
	ErrCode string            `json:"errCode"` // 错误码
	ErrMsg  string            `json:"errMsg"`  // 错误信息
	Data    map[string]string `json:"data"`    //   标准标签号数组
}

func (j *JavaUploadResponse) Error() string {
	return j.ErrMsg
}
