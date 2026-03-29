//go:generate powershell -Command "go tool cgo -godefs types_windows.go | Set-Content -Path ztypes_windows.go -Encoding UTF8"
package devnotify

import (
	"errors"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	modHidsdi                       = windows.NewLazySystemDLL("hid.dll")
	procHidD_GetHidGuid             = modHidsdi.NewProc("HidD_GetHidGuid")
	user32                          = syscall.NewLazyDLL("user32.dll")
	procRegisterDeviceNotificationW = user32.NewProc("RegisterDeviceNotificationW")
)

func getHidGuid() (*windows.GUID, error) {
	var hidGuid windows.GUID
	_, _, err := procHidD_GetHidGuid.Call(
		uintptr(unsafe.Pointer(&hidGuid)),
	)
	if !errors.Is(err, windows.NOERROR) {
		return nil, err
	}

	return &hidGuid, nil
}

func registerDeviceNotification(
	hWnd windows.Handle,
	notificationFilter *_DEV_BROADCAST_DEVICEINTERFACE_W,
	flags uint32,
) (_HDEVNOTIFY, error) {
	r1, _, err := procRegisterDeviceNotificationW.Call(
		uintptr(hWnd),
		uintptr(unsafe.Pointer(notificationFilter)),
		uintptr(flags),
	)
	if r1 == 0 {
		return nil, err
	}

	return _HDEVNOTIFY(unsafe.Pointer(r1)), nil
}

const (
	_DBT_DEVTYP_DEVICEINTERFACE = 0x00000005
)

func RegisterDeviceNotification(hReceiver windows.Handle) error {
	hidGuid, err := getHidGuid()
	if err != nil {
		return err
	}

	notificationFilter := new(_DEV_BROADCAST_DEVICEINTERFACE_W)
	notificationFilter.Size = uint32(unsafe.Sizeof(*notificationFilter))
	notificationFilter.Devicetype = _DBT_DEVTYP_DEVICEINTERFACE
	notificationFilter.Classguid = _GUID(*hidGuid)

	if _, err := registerDeviceNotification(hReceiver, notificationFilter, 0); err != nil {
		return err
	}

	return nil
}
