package main

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type LiveStatus int

const (
	LiveStatusNonLiveContent LiveStatus = iota
	LiveStatusUpcoming
	LiveStatusLiveNow
	LiveStatusEnded
	LiveStatusUnknown
)

type VideoInfo struct {
	PlayabilityStatus struct {
		Status string `json:"status"` // OK, LIVE_STREAM_OFFLINE
		Reason string `json:"reason"`
	} `json:"playabilityStatus"`
	VideoDetails struct {
		VideoID   string `json:"videoId"`
		Title     string `json:"title"`
		ChannelID string `json:"channelId"`
		Author    string `json:"author"`
		Thumbnail struct {
			Thumbnails []struct {
				URL    string `json:"url"`
				Width  int    `json:"width"`
				Height int    `json:"height"`
			} `json:"thumbnails"`
		} `json:"thumbnail"`
		IsLiveContent bool `json:"isLiveContent"`
		IsUpcoming    bool `json:"isUpcoming"`
		IsLive        bool `json:"isLive"`
	} `json:"videoDetails"`
	Microformat struct {
		PlayerMicroformatRenderer struct {
			Thumbnail struct {
				Thumbnails []struct {
					URL    string `json:"url"`
					Width  int    `json:"width"`
					Height int    `json:"height"`
				} `json:"thumbnails"`
			} `json:"thumbnail"`
			LengthSeconds string `json:"lengthSeconds"`
		} `json:"playerMicroformatRenderer"`
		LiveBroadcastDetails struct {
			IsLiveNow      bool       `json:"isLiveNow"`
			StartTimestamp time.Time  `json:"startTimestamp"`
			EndTimestamp   *time.Time `json:"endTimestamp"`
		} `json:"liveBroadcastDetails"`
	} `json:"microformat"`
}

func (info *VideoInfo) LiveStatus() LiveStatus {
	if !info.VideoDetails.IsLiveContent {
		return LiveStatusNonLiveContent
	}

	switch {
	case info.VideoDetails.IsUpcoming:
		return LiveStatusUpcoming
	case info.VideoDetails.IsLive:
		return LiveStatusLiveNow
	case !info.VideoDetails.IsUpcoming && !info.VideoDetails.IsLive:
		return LiveStatusEnded
	default:
		return LiveStatusUnknown
	}
}

func (info *VideoInfo) DownloadThumbnailImage() (io.ReadCloser, error) {
	if len(info.VideoDetails.Thumbnail.Thumbnails) == 0 {
		return nil, fmt.Errorf("no thumbnail image")
	}

	resp, err := http.Get(info.VideoDetails.Thumbnail.Thumbnails[0].URL)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("failed to request thumbnail: %s", resp.Status)
	}
	return resp.Body, nil
}

func GetVideoInfo(id string) (*VideoInfo, error) {
	resp, err := http.Get(getVideoInfoURL(id))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	data, err := url.ParseQuery(string(body))
	if err != nil {
		return nil, err
	}

	status := data.Get("status")
	if status != "ok" {
		reason := data.Get("reason")
		return nil, fmt.Errorf("failed to get player_response: %s", reason)
	}

	playerResponse := data.Get("player_response")
	var info VideoInfo
	if err := json.Unmarshal([]byte(playerResponse), &info); err != nil {
		return nil, err
	}
	if info.PlayabilityStatus.Status == "ERROR" {
		return nil, fmt.Errorf("failed to get video info: %s", info.PlayabilityStatus.Reason)
	}
	return &info, nil
}

func getVideoInfoURL(id string) string {
	return "https://youtube.com/get_video_info?video_id=" + id + "&eurl=https://youtube.googleapis.com/v/" + id
}

var videoRegexpList = []*regexp.Regexp{
	regexp.MustCompile(`(?:v|embed|watch\?v)(?:=|/)([^"&?/=%]{11})`),
	regexp.MustCompile(`(?:=|/)([^"&?/=%]{11})`),
	regexp.MustCompile(`([^"&?/=%]{11})`),
}

func extractVideoID(videoID string) string {
	if strings.Contains(videoID, "youtu") || strings.ContainsAny(videoID, "\"?&/<%=") {
		for _, re := range videoRegexpList {
			if isMatch := re.MatchString(videoID); isMatch {
				subs := re.FindStringSubmatch(videoID)
				videoID = subs[1]
			}
		}
	}

	if strings.ContainsAny(videoID, "?&/<%=") {
		return ""
	}
	if len(videoID) < 10 {
		return ""
	}

	return videoID
}
