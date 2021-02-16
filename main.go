package main

import (
	"context"
	"fmt"
	"github.com/bwmarrin/discordgo"
	log "github.com/sirupsen/logrus"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

type Main struct {
	RecordDir       string
	DiscordBotToken string
	MP4             bool

	dg *discordgo.Session

	waitings     map[string]*LiveStream
	waitingsLock sync.Mutex

	recordings       map[string]*LiveStream
	recordingsLock   sync.Mutex
	wg               sync.WaitGroup
	parentCtx        context.Context
	parentCancelFunc func()
}

func (m *Main) Start() error {
	m.recordings = map[string]*LiveStream{}
	m.waitings = map[string]*LiveStream{}

	dg, err := discordgo.New("Bot " + m.DiscordBotToken)
	if err != nil {
		return err
	}
	dg.Identify.Intents = discordgo.MakeIntent(discordgo.IntentsGuildMessages | discordgo.IntentsGuildMessageReactions)
	dg.AddHandler(m.messageHandler())

	m.parentCtx, m.parentCancelFunc = context.WithCancel(context.Background())

	err = dg.Open()
	if err != nil {
		log.Fatal(err)
	}
	m.dg = dg
	go m.periodicCheck()
	return nil
}

func (m *Main) periodicCheck() {
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			m.waitingsLock.Lock()
			log.Info("periodic check started")

			for videoId, ls := range m.waitings {
				log.Infof("checking %s", videoId)
				if err := ls.ReloadInfo(); err != nil {
					log.Error(err)
					log.Infof("%s was deleted from waiting queue", videoId)
					delete(m.waitings, videoId)
				}
				if ls.Info.LiveStatus() == LiveStatusLiveNow {
					if err := m.startRecording(ls); err != nil {
						log.Error(err)
					}
					delete(m.waitings, ls.ID)
				}
			}

			m.waitingsLock.Unlock()
			log.Info("periodic check finished")
		case <-m.parentCtx.Done():
			return
		}
	}
}

func (m *Main) addWaitingQueue(ls *LiveStream) error {
	m.waitingsLock.Lock()
	defer m.waitingsLock.Unlock()

	if _, ok := m.waitings[ls.ID]; ok {
		return fmt.Errorf("%s has already been queued", ls.ID)
	}
	m.waitings[ls.ID] = ls
	log.Infof("queue added: %s", ls.ID)
	return nil
}

func (m *Main) startRecording(ls *LiveStream) error {
	m.recordingsLock.Lock()
	defer m.recordingsLock.Unlock()
	if _, ok := m.recordings[ls.ID]; ok {
		return fmt.Errorf("%s has already been being recording", ls.ID)
	}
	m.recordings[ls.ID] = ls

	m.wg.Add(1)
	go func() {
		defer m.wg.Done()

		var err error
		if m.MP4 {
			err = ls.RecordAsMP4(m.parentCtx, m.RecordDir)
		} else {
			err = ls.Record(m.parentCtx, m.RecordDir)
		}
		if err != nil {
			log.Error(err)
		}
		log.Infof("live %s recording was stopped", ls.ID)
		_, _ = m.dg.ChannelMessageSend(ls.Msg.ChannelID, fmt.Sprintf("live `%s` recording was finished", ls.ID))

		m.recordingsLock.Lock()
		defer m.recordingsLock.Unlock()
		delete(m.recordings, ls.ID)
	}()
	go func() {
		img, err := ls.Info.DownloadThumbnailImage()
		if err != nil {
			log.WithError(err).Errorf("failed to download the thumbnail image of live %s", ls.ID)
			return
		}
		defer img.Close()

		f, err := os.OpenFile(filepath.Join(m.RecordDir, ls.ID+".jpg"), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, os.ModePerm)
		if err != nil {
			log.WithError(err).Errorf("failed to create the thumbnail image file of live %s", ls.ID)
			return
		}
		defer f.Close()

		_, _ = io.Copy(f, img)
	}()
	log.Infof("live %s recording was started", ls.ID)

	_, _ = m.dg.ChannelMessageSend(ls.Msg.ChannelID, fmt.Sprintf("live `%s` recording was started", ls.ID))
	return nil
}

func (m *Main) cancelRecording(videoId string) error {
	m.waitingsLock.Lock()
	delete(m.waitings, videoId)
	m.waitingsLock.Unlock()

	m.recordingsLock.Lock()
	ls, ok := m.recordings[videoId]
	if ok {
		ls.CancelRecording()
		delete(m.recordings, videoId)
	}
	m.recordingsLock.Unlock()
	return nil
}

func (m *Main) messageHandler() func(*discordgo.Session, *discordgo.MessageCreate) {
	return func(s *discordgo.Session, msg *discordgo.MessageCreate) {
		if msg.Author.ID == s.State.User.ID {
			return
		}

		switch {
		case strings.HasPrefix(msg.Content, "!lssd add"):
			url := strings.TrimSpace(strings.TrimPrefix(msg.Content, "!lssd add "))
			videoId := extractVideoID(url)
			if len(videoId) == 0 {
				if _, err := s.ChannelMessageSend(msg.ChannelID, fmt.Sprintf("invalid live url: %s", url)); err != nil {
					log.Error(err)
				}
				return
			}

			vinfo, err := GetVideoInfo(videoId)
			if err != nil {
				log.Error(err)
				return
			}
			if vinfo.LiveStatus() == LiveStatusNonLiveContent {
				if _, err := s.ChannelMessageSend(msg.ChannelID, fmt.Sprintf("%s is not live content", videoId)); err != nil {
					log.Error(err)
				}
				return
			}

			ls := &LiveStream{
				ID:   videoId,
				Info: vinfo,
				Msg:  msg.Message,
			}
			if vinfo.LiveStatus() == LiveStatusLiveNow {
				if err := m.startRecording(ls); err != nil {
					log.Error(err)
					return
				}
				if err := s.MessageReactionAdd(msg.ChannelID, msg.ID, "🆗"); err != nil {
					log.Error(err)
				}
				return
			}

			if err := m.addWaitingQueue(ls); err != nil {
				if _, err := s.ChannelMessageSend(msg.ChannelID, err.Error()); err != nil {
					log.Error(err)
				}
			}

			if err := s.MessageReactionAdd(msg.ChannelID, msg.ID, "🆗"); err != nil {
				log.Error(err)
			}
		case strings.HasPrefix(msg.Content, "!lssd cancel"):
			url := strings.TrimSpace(strings.TrimPrefix(msg.Content, "!lssd cancel "))
			videoId := extractVideoID(url)
			if len(videoId) == 0 {
				if _, err := s.ChannelMessageSend(msg.ChannelID, fmt.Sprintf("invalid live url: %s", url)); err != nil {
					log.Error(err)
				}
				return
			}

			if err := m.cancelRecording(videoId); err != nil {
				if _, err := s.ChannelMessageSend(msg.ChannelID, err.Error()); err != nil {
					log.Error(err)
				}
				return
			}
			if err := s.MessageReactionAdd(msg.ChannelID, msg.ID, "🆗"); err != nil {
				log.Error(err)
			}
		case strings.HasPrefix(msg.Content, "!lssd list"):
			var sb strings.Builder

			sb.WriteString("**Queued**:\n")
			m.waitingsLock.Lock()
			for id, ls := range m.waitings {
				sb.WriteString(fmt.Sprintf("● `%s` - %s\n", id, ls.Info.VideoDetails.Title))
			}
			m.waitingsLock.Unlock()

			sb.WriteString("**Recordings**:\n")
			m.recordingsLock.Lock()
			for id, ls := range m.recordings {
				sb.WriteString(fmt.Sprintf("● `%s` - %s\n", id, ls.Info.VideoDetails.Title))
			}
			m.recordingsLock.Unlock()

			if _, err := s.ChannelMessageSend(msg.ChannelID, sb.String()); err != nil {
				log.Error(err)
			}
		}
	}
}

func (m *Main) Stop() {
	if m.dg != nil {
		m.dg.Close()
	}
	if m.parentCancelFunc != nil {
		m.parentCancelFunc()
	}
	m.wg.Wait()
}

func main() {
	m := &Main{
		RecordDir:       os.Getenv("RECORD_DIR"),
		DiscordBotToken: os.Getenv("DISCORD_BOT_TOKEN"),
		MP4:             os.Getenv("RECORD_FORMAT") == "mp4",
	}
	if err := m.Start(); err != nil {
		log.Fatal(err)
	}
	go webserver()
	log.Info("lssd has started")

	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt, os.Kill)
	<-sc
	log.Info("signal received")
	m.Stop()
	log.Info("lssd stopped")
}
