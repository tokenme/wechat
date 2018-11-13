package wechatspider

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"github.com/PuerkitoBio/goquery"
	//"github.com/davecgh/go-spew/spew"
	"github.com/mkideal/log"
	"github.com/panjf2000/ants"
	"github.com/tokenme/tmm/common"
	"github.com/tokenme/tmm/tools/qiniu"
	"github.com/tokenme/tmm/utils"
	"github.com/tokenme/wechat/spider"
	"gopkg.in/russross/blackfriday.v2"
	"io/ioutil"
	"net/http"
	"strings"
	"sync"
	"time"
)

type Crawler struct {
	spider  *spider.Spider
	service *common.Service
	config  common.Config
}

func NewCrawler(service *common.Service, config common.Config) *Crawler {
	slack := spider.NewSlack(config.Slack.Token, config.Slack.CaptchaChannel)
	spiderClient := spider.New(slack, service.Redis.Master, config.ProxyApiKey)
	return &Crawler{
		spider:  spiderClient,
		service: service,
		config:  config,
	}
}

func (this *Crawler) Run() error {
	log.Info("Article Crawler start")
	names, err := this.getGzh()
	if err != nil {
		log.Error(err.Error())
		return err
	}
	log.Info("%d Wechat accounts", len(names))
	for _, name := range names {
		count, err := this.GetGzhArticles(name)
		if err != nil {
			log.Error(err.Error())
			continue
		}
		log.Warn("Finished %d articles in %s", count, name)
	}
	return nil
}

func (this *Crawler) getGzh() ([]string, error) {
	db := this.service.Db
	rows, _, err := db.Query(`SELECT name FROM tmm.wx_gzh WHERE updated_at IS NULL OR updated_at<DATE_SUB(NOW(), INTERVAL 1 DAY)`)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, row := range rows {
		names = append(names, row.Str(0))
	}
	return names, nil
}

func (this *Crawler) GetGzhArticles(name string) (int, error) {
	articles, err := this.spider.GetGzhArticles(name)
	if err != nil {
		return 0, err
	}
	log.Warn("Got %d articles in %s", len(articles), name)
	if len(articles) == 0 {
		return 0, nil
	}
	var val []string
	db := this.service.Db
	var ids []string
	for _, a := range articles {
		ids = append(ids, fmt.Sprintf("%d", a.FileId))
	}
	rows, _, err := db.Query(`SELECT fileid FROM tmm.articles WHERE fileid IN (%s)`, strings.Join(ids, ","))
	if err != nil {
		return 0, err
	}
	idMap := make(map[uint64]struct{})
	for _, row := range rows {
		idMap[row.Uint64(0)] = struct{}{}
	}
	for _, a := range articles {
		if _, found := idMap[a.FileId]; found || a.FileId == 0 {
			continue
		}
		newA, err := this.updateArticleImages(a)
		if err != nil {
			log.Error(err.Error())
			continue
		}
		publishTime := time.Unix(1539857334, 0)
		sortId := utils.RangeRandUint64(1, 1000000)
		val = append(val, fmt.Sprintf("(%d, '%s', '%s', '%s', '%s', '%s', '%s', '%s', '%s', %d)", newA.FileId, db.Escape(newA.Author), db.Escape(newA.Title), db.Escape(newA.Url), db.Escape(newA.SourceUrl), db.Escape(newA.Thumbnail), publishTime.Format("2006-01-02 15:04:05"), db.Escape(newA.Digest), db.Escape(newA.Markdown), sortId))
	}
	count := len(val)
	if count > 0 {
		_, _, err := db.Query(`INSERT IGNORE INTO tmm.articles (fileid, author, title, link, source_url, cover, published_at, digest, content, sortid) VALUES %s`, strings.Join(val, ","))
		if err != nil {
			return 0, err
		}
		_, _, err = db.Query(`UPDATE tmm.wx_gzh SET updated_at=NOW() WHERE name='%s'`, db.Escape(name))
		if err != nil {
			return count, err
		}
	}
	return count, nil
}

func (this *Crawler) updateArticleImages(a spider.Article) (spider.Article, error) {
	reader := bytes.NewBuffer(blackfriday.Run([]byte(a.Markdown)))
	doc, err := goquery.NewDocumentFromReader(reader)
	if err != nil {
		return a, err
	}
	var imageMap sync.Map
	var wg sync.WaitGroup
	uploadImagePool, _ := ants.NewPoolWithFunc(10, func(src interface{}) error {
		defer wg.Done()
		ori := src.(string)
		link, err := this.uploadImage(ori)
		if err != nil {
			log.Error(err.Error())
			return err
		}
		imageMap.Store(ori, link)
		return nil
	})
	doc.Find("img").Each(func(idx int, s *goquery.Selection) {
		s.SetAttr("class", "image")
		if src, found := s.Attr("src"); found {
			wg.Add(1)
			uploadImagePool.Serve(src)
		} else {
			s.Remove()
		}
	})
	wg.Wait()
	doc.Find("img").Each(func(idx int, s *goquery.Selection) {
		s.SetAttr("class", "image")
		if src, found := s.Attr("src"); found {
			if link, found := imageMap.Load(src); found {
				s.SetAttr("src", link.(string))
			} else {
				s.Remove()
			}
		} else {
			s.Remove()
		}
	})
	h, err := doc.Find("body").Html()
	if err != nil {
		return a, err
	}
	a.Markdown = h
	return a, nil
}

func (this *Crawler) uploadImage(src string) (string, error) {
	log.Info("Uploading image: %s", src)
	resp, err := http.DefaultClient.Get(src)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	fn := base64.URLEncoding.EncodeToString([]byte(src))
	link, _, err := qiniu.Upload(context.Background(), this.config.Qiniu, this.config.Qiniu.ImagePath, fn, body)
	return link, err
}

func (this *Crawler) Publish() error {
	db := this.service.Db
	rows, _, err := db.Query(`SELECT id, title, digest, cover FROM tmm.articles WHERE published=0 ORDER BY sortid LIMIT 1000`)
	if err != nil {
		return err
	}
	var ids []string
	var val []string
	for _, row := range rows {
		id := row.Uint64(0)
		title := row.Str(1)
		digest := row.Str(2)
		link := fmt.Sprintf("https://tmm.tokenmama.io/article/show/%d", id)
		cover := strings.Replace(row.Str(3), "http://", "https://", -1)
		ids = append(ids, fmt.Sprintf("%d", id))
		val = append(val, fmt.Sprintf("(0, '%s', '%s', '%s', '%s', 100, 100, 1, 10)", db.Escape(title), db.Escape(digest), db.Escape(link), db.Escape(cover)))
	}
	if len(val) > 0 {
		_, _, err := db.Query(`INSERT INTO tmm.share_tasks (creator, title, summary, link, image, points, points_left, bonus, max_viewers) VALUES %s`, strings.Join(val, ","))
		if err != nil {
			return err
		}
		_, _, err = db.Query(`UPDATE tmm.articles SET published=1 WHERE id IN (%s)`, strings.Join(ids, ","))
		if err != nil {
			return err
		}
		log.Info("Published %d articles", len(ids))
	}
	return nil
}
