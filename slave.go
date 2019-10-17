package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/panjf2000/ants"
	"github.com/tidwall/gjson"
	"io/ioutil"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var (
	urls  []string
	wLock sync.Mutex
)

func doSlave() {
	fmt.Println("run as slave")
	gin.SetMode(gin.TestMode)
	urlsFile := gjson.Get(config, "master.urlsFile").String()
	urls = readUrls(urlsFile)

	router := gin.Default()
	router.POST("/stress", stressHandle)

	slaveHost := gjson.Get(config, "slave.host").String()
	fmt.Println("listening", slaveHost)
	_ = router.Run(slaveHost)
}

func readUrls(urlsFile string) []string {
	data, _ := ioutil.ReadFile(urlsFile)
	content := string(data)
	isWin, _ := regexp.MatchString("\r", content)
	lineEnd := "\n"
	if isWin {
		lineEnd = "\r\n"
	}
	return strings.Split(content, lineEnd)
}

type ResultItemType struct {
	HostName   string
	STime      time.Time
	ETime      time.Time
	Cross      time.Duration
	StatusCode int
	Message    string
	RecvBytes  int64
}

func stressHandle(c *gin.Context) {
	param, _ := ioutil.ReadAll(c.Request.Body)
	go doStress(param)
}

func doStress(param []byte) {
	masterHost := gjson.Get(config, "slave.masterHost").String()
	nPerSecond := gjson.GetBytes(param, "nPerSecond").Int()
	total := int(gjson.GetBytes(param, "total").Int())
	parallel := int(gjson.GetBytes(param, "parallel").Int())
	timeOutSec := int(gjson.GetBytes(param, "timeout").Int())
	reqInterval := time.Second / time.Duration(nPerSecond)

	fmt.Printf("nPerSecond:%d total:%d parallel:%d reqInterval(ms):%d\n", nPerSecond, total, parallel, reqInterval.Milliseconds())

	var results []ResultItemType
	errFile, _ := os.OpenFile("err.log", os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0666)
	defer errFile.Close()

	var reqCount int64 = 0
	var errCount int64 = 0
	var okCount int64 = 0
	var reqSum int64 = 0
	var recvBytes int64 = 0
	var errMap sync.Map

	pool, _ := ants.NewPool(parallel)
	defer pool.Release()

	sTime := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < total; i++ {
		u := urls[i%len(urls)]
		wg.Add(1)
		time.Sleep(reqInterval)

		func(url string) {
			_ = pool.Submit(func() {
				httpCli := http.Client{
					Timeout: time.Duration(timeOutSec) * time.Second,
				}
				//start req
				item := ResultItemType{
					STime: time.Now(),
				}
				atomic.AddInt64(&reqCount, 1)
				atomic.AddInt64(&reqSum, 1)

				resp, err := httpCli.Get(url)
				if err != nil {
					item.StatusCode = -1
					item.Message = err.Error()
					atomic.AddInt64(&errCount, 1)
					errMap.Store(err.Error(), "")
				} else {
					item.StatusCode = resp.StatusCode
					item.Message = resp.Status
					if resp.StatusCode == http.StatusOK {
						//response ok
						atomic.AddInt64(&okCount, 1)
						atomic.AddInt64(&recvBytes, resp.ContentLength)
						item.RecvBytes = resp.ContentLength
					} else {
						//response errCode
						atomic.AddInt64(&errCount, 1)
						errBody, _ := ioutil.ReadAll(resp.Body)
						errKey := fmt.Sprintf("%d:%s", resp.StatusCode, resp.Status)
						errVal := fmt.Sprintf("%s", string(errBody))
						errMap.Store(errKey+errVal, "")
						//_ = resp.Body.Close()
					}
				}
				item.ETime = time.Now()
				item.Cross = item.ETime.Sub(item.STime)

				wLock.Lock()
				item.HostName, _ = os.Hostname()
				results = append(results, item)
				wLock.Unlock()
				wg.Done()
			})
		}(u)
	}
	wg.Wait()
	crossSec := time.Since(sTime).Seconds()
	//todo: avgSpeed应该是httpOk的时间
	fmt.Printf("total:%d cross(s):%.1f qps:%d, ok:%d, avgSpeed:%.fMbps, err:%d, errPercent:%.1f%%\n", total, crossSec, int64(total)/int64(crossSec), okCount, float64(recvBytes/1024/1024)*8/crossSec, errCount, float64(errCount)/float64(total)*100)

	errMap.Range(func(key, value interface{}) bool {
		keyStr := key.(string)
		_, _ = errFile.Write([]byte(keyStr + "\n"))
		return true
	})

	url := fmt.Sprintf("http://%s/result", masterHost)
	data, err := json.Marshal(&results)
	if err != nil {
		fmt.Println("err")
	}
	fmt.Println("send result to master, bytes:", len(data))
	_, err = http.Post(url, "application/json", bytes.NewReader(data))
	fmt.Println("post err:", err)
}

func initUrlsHandle(c *gin.Context) {
	data, _ := ioutil.ReadAll(c.Request.Body)
	_ = json.Unmarshal(data, &urls)
	c.JSON(http.StatusOK, nil)
}
