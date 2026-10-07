package audio

import (
	"fmt"
	"unsafe"

	"github.com/gen2brain/malgo"
)

// ListDevices enumerates the system's capture (inputs) and playback
// (outputs) device names. Call it on a user action (settings scene entry,
// voice join), never per frame.
func ListDevices() (inputs, outputs []string, err error) {
	mctx, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return nil, nil, err
	}
	defer func() {
		_ = mctx.Uninit()
		mctx.Free()
	}()
	names := func(t malgo.DeviceType) ([]string, error) {
		infos, err := mctx.Devices(t)
		if err != nil {
			return nil, err
		}
		out := make([]string, 0, len(infos))
		for i := range infos {
			out = append(out, infos[i].Name())
		}
		return out, nil
	}
	if inputs, err = names(malgo.Capture); err != nil {
		return nil, nil, err
	}
	if outputs, err = names(malgo.Playback); err != nil {
		return nil, nil, err
	}
	return inputs, outputs, nil
}

// deviceID looks up the named device of type t on mctx. KNOWN LEAK:
// malgo's DeviceID.Pointer() allocates with C.CBytes and never frees, a few
// bytes per call. So open a device by name once per device change / voice
// join; never per frame or per UI refresh.
func deviceID(mctx *malgo.AllocatedContext, t malgo.DeviceType, name string) (unsafe.Pointer, error) {
	infos, err := mctx.Devices(t)
	if err != nil {
		return nil, err
	}
	for i := range infos {
		if infos[i].Name() == name {
			return infos[i].ID.Pointer(), nil
		}
	}
	return nil, fmt.Errorf("device %q not found", name)
}
