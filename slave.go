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
	urls       []string
	wLock      sync.Mutex
	timeOutSec int
)

func doSlave() {
	fmt.Println("run as slave")
	gin.SetMode(gin.TestMode)
	urlsFile := gjson.Get(config, "master.urlsFile").String()
	urls = readUrls(urlsFile)
	timeOutSec = int(gjson.Get(config, "slave.timeout").Int())

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
	STime      int64
	ETime      int64
	StatusCode int
}

func stressHandle(c *gin.Context) {
	go doStress(c)
}

func doStress(c *gin.Context) {
	param, err := ioutil.ReadAll(c.Request.Body)
	if err != nil {
		fmt.Println(err)
		return
	}
	masterHost := gjson.Get(config, "slave.masterHost").String()
	nPerSecond := gjson.GetBytes(param, "nPerSecond").Int()
	total := int(gjson.GetBytes(param, "total").Int())
	parallel := int(gjson.GetBytes(param, "parallel").Int())
	reqInterval := time.Second / time.Duration(nPerSecond)

	fmt.Printf("nPerSecond:%d total:%d parallel:%d reqInterval(ms):%d\n", nPerSecond, total, parallel, reqInterval.Milliseconds())

	var results []ResultItemType
	errFile, _ := os.OpenFile("err.log", os.O_CREATE|os.O_TRUNC, 0644)
	defer errFile.Close()

	var reqCount int64 = 0
	var errCount int64 = 0
	var okCount int64 = 0
	var reqSum int64 = 0
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
					STime: time.Now().UnixNano(),
				}
				atomic.AddInt64(&reqCount, 1)
				atomic.AddInt64(&reqSum, 1)

				resp, err := httpCli.Get(url)
				if err != nil {
					item.StatusCode = -1
					atomic.AddInt64(&errCount, 1)
					errMap.Store(err.Error(), "")
				} else {
					item.StatusCode = resp.StatusCode
					if resp.StatusCode == http.StatusOK {
						//response ok
						atomic.AddInt64(&okCount, 1)
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
				item.ETime = time.Now().UnixNano()

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
	fmt.Printf("total:%d cross(ms):%f qps:%d, ok:%d, err:%d\n", total, crossSec*1000, okCount/int64(crossSec), okCount, errCount)

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
	fmt.Println(err)
}

func initUrlsHandle(c *gin.Context) {
	data, _ := ioutil.ReadAll(c.Request.Body)
	_ = json.Unmarshal(data, &urls)
	c.JSON(http.StatusOK, nil)
}
