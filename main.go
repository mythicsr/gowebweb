package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"os"
)

var (
	h          bool
	isMaster   bool
	isSendFile bool
	config     string
)

func init() {
	flag.BoolVar(&h, "h", false, "this help")
	flag.BoolVar(&isMaster, "m", false, "run as Master")
	flag.BoolVar(&isSendFile, "s", false, "dispatch master's urlsFile to slaves")
	flag.Usage = func() {
		_, _ = fmt.Fprintf(flag.CommandLine.Output(), "Usage of %s:\n", os.Args[0])
		flag.PrintDefaults()
		fmt.Println("master:gowebweb -m")
		fmt.Println("slave:gowebweb ")
	}
}

func main() {
	flag.Parse()

	if h {
		flag.Usage()
		return
	}

	data, _ := ioutil.ReadFile("config.json")
	config = string(data)

	if isMaster {
		doMaster()
	} else {
		doSlave()
	}
}
