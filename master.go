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

	for _, s := range slaves {
		fmt.Println("start stress ->", s)
		url := fmt.Sprintf("http://%s/stress", s)
		param := gjson.Get(config, "master.param").String()

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

	//sTime
	sort.Slice(results, func(i, j int) bool {
		return results[i].STime < results[j].STime
	})
	sTime := time.Unix(0, results[0].STime)

	//eTime
	sort.Slice(results, func(i, j int) bool {
		return results[i].ETime > results[j].ETime
	})
	eTime := time.Unix(0, results[0].ETime)

	total := len(results)

	okCount := 0
	for _, r := range results {
		if r.StatusCode == http.StatusOK {
			okCount++
		}
	}

	errCount := total - okCount
	crossSec := eTime.Sub(sTime).Seconds()

	fmt.Printf("total:%d cross(s):%f qps:%d, ok:%d, err:%d(%f%)\n", total, crossSec, int64(okCount)/int64(crossSec), okCount, errCount, float64(errCount)/float64(total)*100)
	//fmt.Printf("req-cross:  min:%dms max:%dms avg:%dms\n",)

	resultFile, err := os.OpenFile("results.log", os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0666)
	if err != nil {
		panic(err)
	}
	for _, r := range results {
		fmt.Fprintf(resultFile, "%s %d %d %d\n", r.HostName, r.STime, r.ETime, r.StatusCode)
	}
	resultFile.Close()
	fmt.Println("please check results.log")
}
