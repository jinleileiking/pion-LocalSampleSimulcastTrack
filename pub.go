package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	log "git.visionular.com/cloud/cloudlive.git/pkg/utils/logger"
	"git.visionular.com/cloud/cloudlive.git/pkg/wzrtc/client"
	"git.visionular.com/cloud/cloudlive.git/pkg/wzrtc/constant"
	"github.com/pion/webrtc/v3"
	"github.com/pion/webrtc/v3/pkg/media"
	"github.com/pion/webrtc/v3/pkg/media/ivfreader"
	"github.com/pion/webrtc/v3/pkg/media/oggreader"
	"github.com/spf13/cobra"
)

var pubCmd = &cobra.Command{
	Use:   "pub",
	Short: "publish",
	Run: func(cmd *cobra.Command, args []string) {
		for {
			if err := pub(addr, pubVar.File, name); err != nil {
				panic(err)
			}
			if !pubVar.Loop {
				break
			}
		}
	},
}

type pubCmdVar struct {
	File string
	Loop bool
}

var pubVar pubCmdVar

func initPubCmd() {
	pubCmd.Flags().StringVarP(&pubVar.File, "file", "", "test/jing", "filename prefix. Eg: jing. Program will find jing240.ivf, jing360.ivf, jing480.ivf, jing.ogg")
	pubCmd.Flags().BoolVar(&pubVar.Loop, "loop", false, "loop")
}

func pub(addr string, f string, name string) error {
	conn, err := grpc.Dial(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		panic(err)
	}
	client, err := client.NewClient(conn)
	if err != nil {
		panic(err)
	}

	sidA := "sid-audio-" + name
	sidV := "sid-vudio-" + name

	// pc, err := client.Publish(name, []string{tlA.ID(), tlVL.ID(), tlVM.ID(), tlVH.ID()})
	pc, err := client.Publish(name, []string{sidA, sidV})
	if err != nil {
		panic(err)
	}

	tlA, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus}, "tid-audio-"+name, sidA)
	if err != nil {
		panic(err)
	}

	if _, err := pc.AddTrack(tlA); err != nil {
		panic(err)
	}

	tlVL, err := NewLocalSimulcastSampleTrack(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8}, "video-"+name, "rid-l-"+name)
	if err != nil {
		panic(err)
	}
	var sender *webrtc.RTPSender

	if sender, err = pc.AddTrack(tlVL); err != nil {
		panic(err)
	}

	tlVM, err := NewLocalSimulcastSampleTrack(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8}, "video-"+name, "rid-m-"+name)
	if err != nil {
		panic(err)
	}

	if err := sender.AddEncoding(tlVM); err != nil {
		panic(err)
	}

	tlVH, err := NewLocalSimulcastSampleTrack(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8}, "video-"+name, "rid-h-"+name)
	if err != nil {
		panic(err)
	}

	if err := sender.AddEncoding(tlVH); err != nil {
		panic(err)
	}

	// tlVL, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8}, "tid-video-"+name, sidV, webrtc.WithRTPStreamID("rid-l-"+name))
	// if err != nil {
	// 	panic(err)
	// }
	// var sender *webrtc.RTPSender
	//
	// if sender, err = pc.AddTrack(tlVL); err != nil {
	// 	panic(err)
	// }
	//
	// tlVM, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8}, "tid-video-"+name, sidV, webrtc.WithRTPStreamID("rid-m-"+name))
	// if err != nil {
	// 	panic(err)
	// }
	//
	// if err := sender.AddEncoding(tlVM); err != nil {
	// 	panic(err)
	// }
	//
	// tlVH, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8}, "tid-video-"+name, sidV, webrtc.WithRTPStreamID("rid-h-"+name))
	// if err != nil {
	// 	panic(err)
	// }
	//
	// if err := sender.AddEncoding(tlVH); err != nil {
	// 	panic(err)
	// }

	// log.Printf("publishing")

	g := new(errgroup.Group)

	g.Go(func() error { return writeIvf(f+"240", tlVL) })
	g.Go(func() error { return writeIvf(f+"360", tlVM) })
	g.Go(func() error { return writeIvf(f+"480", tlVH) })
	g.Go(func() error { return writeOgg(f, tlA) })

	if err := g.Wait(); err == nil {
		return fmt.Errorf("g.Wait failed: %w", err)
	}

	return nil
}

func writeOgg(f string, tl *webrtc.TrackLocalStaticSample) error {
	file, err := os.Open(f + ".ogg")
	if err != nil {
		return fmt.Errorf("os.Open failed: %w", err)
	}

	ogg, _, err := oggreader.NewWith(file)
	if err != nil {
		return fmt.Errorf("oggreader.NewWith failed: %w", err)
	}

	oggPageDuration := time.Millisecond * 20

	var lastGranule uint64

	log.GetLogger().InfoFields(context.Background(), nil, "start send audio sample")

	// It is important to use a time.Ticker instead of time.Sleep because
	// * avoids accumulating skew, just calling time.Sleep didn't compensate for the time spent parsing the data
	// * works around latency issues with Sleep (see https://github.com/golang/go/issues/44343)
	ticker := time.NewTicker(oggPageDuration)
	for ; true; <-ticker.C {
		pageData, pageHeader, err := ogg.ParseNextPage()
		if errors.Is(err, io.EOF) {
			return nil
		}

		if err != nil {
			return fmt.Errorf("ogg.ParseNextPage failed: %w", err)
		}

		// The amount of samples is the difference between the last and current timestamp
		sampleCount := float64(pageHeader.GranulePosition - lastGranule)
		lastGranule = pageHeader.GranulePosition
		sampleDuration := time.Duration((sampleCount/48000)*1000) * time.Millisecond

		if err := tl.WriteSample(media.Sample{Data: pageData, Duration: sampleDuration}); err != nil {
			return fmt.Errorf("tlA.WriteSample failed: %w", err)
		}
	}

	return nil
}

func writeIvf(f string, tl *LocalSampleSimulcastTrack) error {
	file, err := os.Open(f + ".ivf")
	if err != nil {
		return fmt.Errorf("os.Open failed: %w", err)
	}

	ivf, header, err := ivfreader.NewWith(file)
	if err != nil {
		return fmt.Errorf("ivfreader.NewWith failed: %w", err)
	}

	ticker := time.NewTicker(time.Millisecond * time.Duration((float32(header.TimebaseNumerator)/float32(header.TimebaseDenominator))*1000))
	log.GetLogger().InfoFields(context.Background(), nil, "start send video sample")

	logged := false
	for range ticker.C {
		frame, _, err := ivf.ParseNextFrame()
		if errors.Is(err, io.EOF) {
			return nil
		}

		if err != nil {
			return fmt.Errorf("ivf.ParseNextFrame failed: %w", err)
		}

		if !logged {
			log.GetLogger().InfoFields(context.Background(), map[string]interface{}{constant.Module: constant.ModuleTrack}, "sending video....")
			logged = true
		}

		if err = tl.WriteSample(media.Sample{Data: frame, Duration: time.Second}, nil); err != nil {
			// if err = tlV.WriteSample(media.Sample{Data: frame, Duration: delta}); err != nil {
			return fmt.Errorf("tl.WriteSample failed: %w", err)
		}
	}

	return nil
}
