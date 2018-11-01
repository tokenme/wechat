package spider

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/PuerkitoBio/goquery"
	//"github.com/davecgh/go-spew/spew"
	"github.com/garyburd/redigo/redis"
	"github.com/levigross/grequests"
	"log"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type ArticleResult struct {
	Article *Article `json:"app_msg_ext_info"`
	Common  *Article `json:"comm_msg_info"`
}

type Article struct {
	Title     string    `json:"title"`
	Url       string    `json:"content_url"`
	Thumbnail string    `json:"cover"`
	Date      int       `json:"datetime"`
	SourceUrl string    `json:"source_url"`
	Digest    string    `json:"digest"`
	Items     []Article `json:"multi_app_msg_item_list"`
}

type Spider struct {
	slackBot    *Slack
	redisClient *redis.Pool
	httpClient  *grequests.Session
}

func New(slackBot *Slack, redisClient *redis.Pool) *Spider {
	ro := &grequests.RequestOptions{
		UserAgent:    "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_9_2) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/34.0.1847.116 Safari/537.36",
		UseCookieJar: true,
	}
	return &Spider{
		slackBot:    slackBot,
		redisClient: redisClient,
		httpClient:  grequests.NewSession(ro),
	}
}

func (this *Spider) GetGzhArticles(wechatName string) ([]Article, error) {
	profile, err := this.getProfile(wechatName)

	if err != nil {
		log.Println(err)
		return nil, err
	}

	if profile == "" {
		return nil, fmt.Errorf("%s", "公众号不存在或代理失效!")
	}
	resp, err := this.wxOpenUnlock(profile, "http://weixin.sogou.com/weixin?type=1&query="+wechatName+"&ie=utf8&_sug_=n&_sug_type_=")
	if err != nil {
		log.Println(err)
		return nil, err
	}

	r, _ := regexp.Compile("var msgList = {\"list\":\\[([\\s\\S]*?)\\]}")
	result := r.FindAllSubmatch(resp.Bytes(), -1)
	if len(result) == 0 {
		return nil, errors.New("not found")
	}
	var buffer bytes.Buffer
	buffer.Write([]byte("["))
	buffer.Write(result[0][1])
	buffer.Write([]byte("]"))
	msgList := buffer.Bytes()
	var articlesResult []ArticleResult
	err = json.Unmarshal(msgList, &articlesResult)
	if err != nil {
		return nil, err
	}
	var articles []Article
	for _, ret := range articlesResult {
		if ret.Common == nil || ret.Article == nil {
			continue
		}
		date := ret.Common.Date
		if ret.Article.Title != "" {
			a := Article{
				Title:     ret.Article.Title,
				Digest:    ret.Article.Digest,
				Url:       fmt.Sprintf("https://mp.weixin.qq.com%s", ret.Article.Url),
				Thumbnail: ret.Article.Thumbnail,
				Date:      date,
			}
			articles = append(articles, a)
		}
		if ret.Article.Items == nil {
			continue
		}
		for _, i := range ret.Article.Items {
			a := Article{
				Title:     i.Title,
				Digest:    i.Digest,
				Url:       fmt.Sprintf("https://mp.weixin.qq.com%s", i.Url),
				Thumbnail: i.Thumbnail,
				Date:      date,
			}
			articles = append(articles, a)
		}
	}
	return articles, nil
}

func (this *Spider) getProfile(name string) (string, error) {
	var ro *grequests.RequestOptions
	proxyUrl, _ := GetProxy(this.redisClient)
	if proxyUrl != nil {
		ro = &grequests.RequestOptions{
			Proxies: map[string]*url.URL{"https": proxyUrl},
		}
	}
	resp, err := this.httpClient.Get("http://weixin.sogou.com/weixin?type=1&query="+name+"&ie=utf8&_sug_=n&_sug_type_=", ro)
	if err != nil {
		return "", err
	}
	doc, err := goquery.NewDocumentFromResponse(resp.RawResponse)

	if err != nil {
		return "", err
	}
	profile := ""

	doc.Find(".news-box li p.tit a").EachWithBreak(func(i int, s *goquery.Selection) bool {
		// For each item found, get the band and title
		weixinhao := s.Find("em").Text()
		if weixinhao == name {
			profile, _ = s.Attr("href")
			return false
		}
		return true
	})

	//如果没有则默认取第一个

	if profile == "" {
		profile, _ = doc.Find("li p.tit a").Eq(0).Attr("href")
	}

	return profile, nil
}

func (this *Spider) wxOpenUnlock(link string, referrer string) (*grequests.Response, error) {
	ro := &grequests.RequestOptions{
		Headers: map[string]string{
			"Referer": referrer,
		},
	}
	proxyUrl, _ := GetProxy(this.redisClient)
	if proxyUrl != nil {
		ro.Proxies = map[string]*url.URL{"https": proxyUrl}
	}
	resp, err := this.httpClient.Get(link, ro)
	if err != nil {
		log.Println(err)
		return nil, err
	}
	body := resp.String()
	if strings.Contains(body, "请输入验证码") {
		err := this.tryUnlockWx(link, referrer)
		if err != nil {
			return nil, err
		} else {
			return this.wxOpenUnlock(link, referrer)
		}
	}
	return resp, nil
}

func (this *Spider) tryUnlockWx(link string, referrer string) error {
	var ro *grequests.RequestOptions
	proxyUrl, _ := GetProxy(this.redisClient)
	if proxyUrl != nil {
		ro = &grequests.RequestOptions{
			Proxies: map[string]*url.URL{"https": proxyUrl},
		}
	}
	log.Println(fmt.Sprintf("https://mp.weixin.qq.com/mp/verifycode?cert=%d", time.Now().UnixNano()))
	resp, err := this.httpClient.Get(fmt.Sprintf("https://mp.weixin.qq.com/mp/verifycode?cert=%d", time.Now().UnixNano()), ro)
	if err != nil {
		log.Println(err)
		return err
	}
	return this.unlockWxVerify(link, resp.Bytes())
}

func (this *Spider) unlockWxVerify(link string, image []byte) error {
	log.Println("Try to unlock wechat")
	file, err := this.slackBot.UploadFile(image)
	if err != nil {
		log.Println(err)
		return err
	}
	defer this.slackBot.DeleteFile(file.ID)
	log.Println("Uploaded file: ", file.ID)
	checkTicker := time.NewTicker(2 * time.Second)
	codeCh := make(chan string, 1)
	defer close(codeCh)
	for {
		select {
		case <-checkTicker.C:
			finfo, _, err := this.slackBot.GetFile(file.ID)
			if err != nil {
				log.Println(err)
				break
			} else if finfo.Title != finfo.Name {
				checkTicker.Stop()
				codeCh <- finfo.Title
			}
		case code := <-codeCh:
			log.Println("get code: ", code)
			ro := &grequests.RequestOptions{
				Data: map[string]string{
					"cert":  strconv.FormatInt(time.Now().UnixNano(), 10),
					"input": code,
				},
				Headers: map[string]string{
					"Host":    "mp.weixin.qq.com",
					"Referer": link,
				},
			}
			resp, err := this.httpClient.Post("https://mp.weixin.qq.com/mp/verifycode", ro)
			if err != nil {
				log.Println(err)
				return err
			}
			if resp.Ok {
				return nil
			} else {
				return errors.New("verfiy code failed")
			}
		}
	}
	return nil
}
