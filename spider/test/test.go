package main

import (
	"github.com/davecgh/go-spew/spew"
	"github.com/tokenme/wechat/spider"
	"log"
)

func main() {
	slack := spider.NewSlack("xoxp-340014960567-338241194720-339563622341-94fcb61ce9353b2b0f5a86d4e99580d8", "captcha")
	redis := spider.NewRedisPool("tokenme-001.0gguww.0001.apne1.cache.amazonaws.com:6379", 10, 120)
	client := spider.New(slack, redis)
	articles, err := client.GetGzhArticles("擒牛股票工作室")
	if err != nil {
		log.Fatalln(err)
	}
	spew.Dump(articles)
}
