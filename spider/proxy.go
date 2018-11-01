package spider

import (
	"fmt"
	"github.com/garyburd/redigo/redis"
	"io/ioutil"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	_PROXY_API_GATEWAY = "http://api.ip.data5u.com/dynamic/get.html?order=%s&sep=3"
	_PROXY_CACHE_KEY   = "data:5u:proxy"
)

type Proxy struct {
	cache  *redis.Pool
	apiKey string
}

func NewProxy(service *redis.Pool, apiKey string) *Proxy {
	return &Proxy{
		cache:  service,
		apiKey: apiKey,
	}
}

func (this *Proxy) Get() (proxyURL *url.URL, err error) {
	var proxy string
	if this.cache != nil {
		redisConn := this.cache.Get()
		defer redisConn.Close()
		proxy, err = redis.String(redisConn.Do("GET", _PROXY_CACHE_KEY))
		if err != nil {
			proxy, err = this.Update()
			if proxy == "" || err != nil {
				return nil, err
			}
			redisConn.Do("SETEX", _PROXY_CACHE_KEY, 50, proxy)
		}
	} else {
		proxy, err = this.Update()
		if err != nil || proxy == "" {
			return nil, err
		}
	}
	proxy = strings.TrimSpace(proxy)
	return url.Parse("http://" + proxy)
}

func (this *Proxy) Update() (proxy string, err error) {
	resp, err := http.Get(fmt.Sprintf(_PROXY_API_GATEWAY, this.apiKey))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	reg := regexp.MustCompile("\\d+\\.\\d+\\.\\d+\\.\\d+:\\d+")
	if err != nil {
		return "", err
	}
	if reg.Match(body) {
		return strings.TrimSpace(string(body)), nil
	}
	return "", nil
}

func NewRedisPool(server string, maxIdle int, idleTime time.Duration) *redis.Pool {
	return &redis.Pool{
		MaxIdle:     maxIdle,
		IdleTimeout: idleTime * time.Second,
		Dial: func() (redis.Conn, error) {
			c, err := redis.Dial("tcp", server)
			if err != nil {
				return nil, err
			}
			/*if _, err := c.Do("AUTH", password); err != nil {
			    c.Close()
			    return nil, err
			}*/
			return c, err
		},
		TestOnBorrow: func(c redis.Conn, t time.Time) error {
			_, err := c.Do("PING")
			return err
		},
	}
}
