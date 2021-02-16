package main

import (
	"context"
	"fmt"
	"github.com/bwmarrin/discordgo"
	log "github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
	"os"
	"os/exec"
	"path/filepath"
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
		"--loglevel", "info",
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

func (ls *LiveStream) RecordAsMP4(ctx context.Context, recordDir string) error {
	filename := genTSFileName(ls)

	logS, err := os.Create(filepath.Join(recordDir, filename+".0.log"))
	if err != nil {
		return err
	}
	defer logS.Close()

	logF, err := os.Create(filepath.Join(recordDir, filename+".1.log"))
	if err != nil {
		return err
	}
	defer logF.Close()

	cmdS := exec.Command("streamlink",
		"--loglevel", "info",
		"--hls-live-restart",
		"-O",
		"https://www.youtube.com/watch?v="+ls.ID, "best")
	cmdS.Stderr = logS
	pout, err := cmdS.StdoutPipe()
	if err != nil {
		return err
	}

	cmdF := exec.Command("ffmpeg",
		"-i", "-",
		"-c", "copy",
		filepath.Join(recordDir, filename+".mp4"))
	cmdF.Stdout = logF
	cmdF.Stderr = logF
	cmdF.Stdin = pout

	if err := cmdS.Start(); err != nil {
		return err
	}
	if err := cmdF.Start(); err != nil {
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
			if err := cmdS.Process.Signal(os.Interrupt); err != nil {
				log.Error(err)
			}
			if err := cmdF.Process.Signal(os.Interrupt); err != nil {
				log.Error(err)
			}
			return
		}
	}()

	var eg errgroup.Group
	eg.Go(func() error { return cmdS.Wait() })
	eg.Go(func() error { return cmdF.Wait() })
	return eg.Wait()
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
	return fmt.Sprintf("%s.ts", ls.ID)
}
