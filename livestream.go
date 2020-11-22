package main

import (
	"context"
	"fmt"
	"github.com/bwmarrin/discordgo"
	log "github.com/sirupsen/logrus"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

type LiveStream struct {
	ID   string
	Info *VideoInfo
	Msg  *discordgo.Message

	ctx        context.Context
	cancelFunc func()
}

func (ls *LiveStream) Record(ctx context.Context, recordDir string) error {
	filename := genTSFileName(ls)

	logF, err := os.Create(filepath.Join(recordDir, filename+".log"))
	if err != nil {
		return err
	}
	defer logF.Close()

	cmd := exec.Command("streamlink",
		"--loglevel", "trace",
		"--hls-live-restart",
		"-o", filepath.Join(recordDir, filename),
		"https://www.youtube.com/watch?v="+ls.ID, "best")
	cmd.Stdout = logF
	cmd.Stderr = logF

	if err := cmd.Start(); err != nil {
		return err
	}

	ctx, f := context.WithCancel(ctx)
	ls.ctx = ctx
	ls.cancelFunc = f

	stopped := make(chan struct{})
	defer func() {
		close(stopped)
		ls.cancelFunc()
	}()

	go func() {
		select {
		case <-stopped:
			return
		case <-ctx.Done():
			if err := cmd.Process.Signal(os.Interrupt); err != nil {
				log.Error(err)
			}
			return
		}
	}()
	return cmd.Wait()
}

func (ls *LiveStream) CancelRecording() {
	if ls.cancelFunc != nil {
		ls.cancelFunc()
	}
}

func (ls *LiveStream) ReloadInfo() (err error) {
	ls.Info, err = GetVideoInfo(ls.ID)
	return err
}

func genTSFileName(ls *LiveStream) string {
	return fmt.Sprintf("youtube_%s_%d.ts", ls.ID, time.Now().Unix())
}
