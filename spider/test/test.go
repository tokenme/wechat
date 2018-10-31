package main

import (
	"github.com/davecgh/go-spew/spew"
	"github.com/tokenme/wechat/spider"
	"log"
)

func main() {
	articles, err := spider.Spider("擒牛股票工作室", nil)
	if err != nil {
		log.Fatalln(err)
	}
	spew.Dump(articles)
}
