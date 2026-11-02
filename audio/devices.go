package audio

import (
	"errors"
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

// ErrDeviceLost means a device switch failed and the previous device would
// not restart either: the stream has no running device until it is rejoined.
var ErrDeviceLost = errors.New("audio device stopped")

// switchDevice moves a running stream from old to nd (initialised, not
// started): old is stopped, quiesced runs while neither device is running (nil
// = nothing), nd is started, then old is freed. If nd cannot start, nd is
// freed and old is restarted, so a failed switch keeps the previous device
// running; if old cannot restart either the error wraps ErrDeviceLost. The
// stream has a short gap while neither runs.
func switchDevice(old, nd *malgo.Device, quiesced func()) error {
	// miniaudio's ma_device_stop returns success for a device that is already
	// stopped and an error only when the backend fails to stop it, as with an
	// unplugged device. Such an old device is being replaced anyway, so the
	// error is not a reason to abandon the swap.
	_ = old.Stop()
	if quiesced != nil {
		quiesced()
	}
	if err := nd.Start(); err != nil {
		nd.Uninit()
		if rerr := old.Start(); rerr != nil {
			return fmt.Errorf("%w: %v (previous device did not restart: %v)", ErrDeviceLost, err, rerr)
		}
		return err
	}
	old.Uninit() // waits for the old callback to finish
	return nil
}
