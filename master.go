package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"io/ioutil"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var slaves []string
var m sync.Mutex
var results []ResultItemType
var remain int32

func doMaster() {
	fmt.Println("run as master")
	for _, r := range gjson.Get(config, "master.slaves").Array() {
		slaves = append(slaves, r.String())
	}
	remain = int32(len(slaves))
	go runRouter()

	//if isSendFile {
	//	initSlaveUrls(urls, slaves)
	//}
	startStressSlaves()
	select {}
}

func initSlaveUrls(urls []string, slaves []string) {
	data, _ := json.Marshal(&urls)
	for _, s := range slaves {
		fmt.Println("send urlsFile to", s)
		url := fmt.Sprintf("http://%s/initUrls", s)
		_, err := http.Post(url, "application/json", bytes.NewReader(data))
		if err != nil {
			panic(err)
		}
	}
}

func startStressSlaves() {
	time.Sleep(500 * time.Millisecond)

	go func() {
		if atomic.LoadInt32(&remain) == 0 {
			genResults()
		}
	}()

	param := gjson.Get(config, "master.param").String()
	fmt.Println("req param:", param)

	for _, s := range slaves {
		fmt.Println("start stress ->", s)
		url := fmt.Sprintf("http://%s/stress", s)
		_, err := http.Post(url, "application/json", strings.NewReader(param))
		if err != nil {
			panic(err)
		}
	}
}

func runRouter() {
	gin.SetMode(gin.TestMode)
	router := gin.Default()
	router.POST("/result", resultHandle)
	masterHost := gjson.Get(config, "master.host").String()
	fmt.Println("listening", masterHost)
	router.Run(masterHost)
}

func resultHandle(c *gin.Context) {
	var rs []ResultItemType
	data, _ := ioutil.ReadAll(c.Request.Body)
	_ = json.Unmarshal(data, &rs)
	m.Lock()
	results = append(results, rs...)
	m.Unlock()
	fmt.Println("len=", len(results))

	if atomic.AddInt32(&remain, -1) == 0 {
		genResults()
	}
}

func genResults() {
	fmt.Println("finish test, gen results")
	statusMap := make(map[int]int)
	var recvBytes int64
	reqMinCross := 1000 * time.Second
	reqMaxCross := 1 * time.Nanosecond
	var reqAvgCross time.Duration
	var reqSumCross time.Duration

	//sTime
	sort.Slice(results, func(i, j int) bool {
		return results[i].STime.Before(results[j].STime)
	})
	sTime := results[0].STime

	//eTime
	sort.Slice(results, func(i, j int) bool {
		return results[i].ETime.After(results[j].ETime)
	})
	eTime := results[0].ETime

	total := len(results)

	okCount := 0
	for _, r := range results {
		if r.StatusCode == http.StatusOK {
			okCount++

			if r.Cross < reqMinCross {
				reqMinCross = r.Cross
			}
			if r.Cross > reqMaxCross {
				reqMaxCross = r.Cross
			}
			reqSumCross += r.Cross
			recvBytes += r.RecvBytes
		} else {
			statusMap[r.StatusCode]++
		}
	}

	reqAvgCross = reqSumCross / time.Duration(len(results))

	errCount := total - okCount
	crossSec := eTime.Sub(sTime).Seconds()

	//print info
	fmt.Printf("total:%d cross(s):%.1f qps:%d, ok:%d, err:%d, errPercent:%.1f%%\n", total, crossSec, int64(total)/int64(crossSec), okCount, errCount, float64(errCount)/float64(total)*100)
	fmt.Printf("httpOK: reqAvgCross:%dms, reqMinCross:%dms, reqMaxCross:%dms avgSpeed:%.1fMbps\n", reqAvgCross.Milliseconds(), reqMinCross.Milliseconds(), reqMaxCross.Milliseconds(), float64(recvBytes/1024/1024)*8/crossSec)

	fmt.Println("errInfo: (-1 means timeout)")
	for httpCode, count := range statusMap {
		fmt.Printf("httpCode:%d count:%d\n", httpCode, count)
	}

	//write results.log
	resultFile, err := os.OpenFile("results.log", os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0666)
	if err != nil {
		panic(err)
	}
	for _, r := range results {
		_, _ = fmt.Fprintf(resultFile, "%s %d %d %d %d\n", r.HostName, r.STime.Unix(), r.ETime.Unix(), r.Cross.Milliseconds(), r.StatusCode)
	}
	_ = resultFile.Close()
	fmt.Println("please check results.log")
}
