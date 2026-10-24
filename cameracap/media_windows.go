//go:build windows

package cameracap

import (
	"errors"
	"fmt"
	"image"

	"github.com/pion/mediadevices"
	"github.com/pion/mediadevices/pkg/driver"
	_ "github.com/pion/mediadevices/pkg/driver/camera"
	mediavideo "github.com/pion/mediadevices/pkg/io/video"
	"github.com/pion/mediadevices/pkg/prop"
)

const Supported = true

func Devices() ([]Device, error) {
	drivers := driver.GetManager().Query(driver.FilterAnd(
		driver.FilterVideoRecorder(),
		driver.FilterNot(driver.FilterDeviceType(driver.Screen)),
	))
	devices := make([]Device, 0, len(drivers))
	for _, camera := range drivers {
		info := camera.Info()
		name := info.Name
		if name == "" {
			name = info.Label
		}
		if name == "" {
			name = camera.ID()
		}
		devices = append(devices, Device{ID: camera.ID(), Name: name})
	}
	return devices, nil
}

func Open(deviceID string) (Source, error) {
	stream, err := mediadevices.GetUserMedia(mediadevices.MediaStreamConstraints{
		Video: func(c *mediadevices.MediaTrackConstraints) {
			c.Width = prop.Int(Width)
			c.Height = prop.Int(Height)
			c.FrameRate = prop.Float(FPS)
			if deviceID != "" {
				c.DeviceID = prop.StringExact(deviceID)
			}
		},
	})
	if err != nil {
		return nil, fmt.Errorf("cameracap: open camera: %w", err)
	}
	tracks := stream.GetVideoTracks()
	if len(tracks) == 0 {
		return nil, errors.New("cameracap: camera returned no video track")
	}
	track, ok := tracks[0].(*mediadevices.VideoTrack)
	if !ok {
		_ = tracks[0].Close()
		return nil, errors.New("cameracap: unsupported video track")
	}
	return newLatestFrameSource(&mediaReader{reader: track.NewReader(false), track: track}), nil
}

type mediaReader struct {
	reader mediavideo.Reader
	track  *mediadevices.VideoTrack
}

func (r *mediaReader) Read() (image.Image, func(), error) { return r.reader.Read() }
func (r *mediaReader) Close() error                       { return r.track.Close() }
