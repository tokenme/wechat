package spider

import (
	"bytes"
	"fmt"
	"github.com/nlopes/slack"
	"time"
)

type Slack struct {
	client  *slack.Client
	channel string
}

func NewSlack(token string, channel string) *Slack {
	return &Slack{
		client:  slack.New(token),
		channel: channel,
	}
}

func (this *Slack) UploadFile(data []byte) (*slack.File, error) {
	params := slack.FileUploadParameters{
		Filename: fmt.Sprintf("wx-verify-code:%d", time.Now().Unix()),
		Filetype: "jpg",
		Channels: []string{this.channel},
		Reader:   bytes.NewReader(data),
	}
	return this.client.UploadFile(params)
}

func (this *Slack) GetFile(id string) (*slack.File, []slack.Comment, error) {
	file, comments, _, err := this.client.GetFileInfo(id, 0, 0)
	return file, comments, err
}

func (this *Slack) DeleteFile(id string) error {
	return this.client.DeleteFile(id)
}
