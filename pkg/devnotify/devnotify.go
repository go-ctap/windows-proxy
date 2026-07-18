//go:generate powershell -Command "go tool cgo -godefs types_windows.go | gofmt | Set-Content -Path ztypes_windows.go -Encoding Ascii"
package devnotify

import (
	"errors"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	modHidsdi                        = windows.NewLazySystemDLL("hid.dll")
	procHidD_GetHidGuid              = modHidsdi.NewProc("HidD_GetHidGuid")
	user32                           = syscall.NewLazyDLL("user32.dll")
	procRegisterDeviceNotificationW  = user32.NewProc("RegisterDeviceNotificationW")
	procUnregisterDeviceNotification = user32.NewProc("UnregisterDeviceNotification")
)

type DeviceNotification uintptr

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
) (DeviceNotification, error) {
	r1, _, err := procRegisterDeviceNotificationW.Call(
		uintptr(hWnd),
		uintptr(unsafe.Pointer(notificationFilter)),
		uintptr(flags),
	)
	if r1 == 0 {
		return 0, err
	}

	return DeviceNotification(r1), nil
}

const (
	_DBT_DEVTYP_DEVICEINTERFACE   = 0x00000005
	_DEVICE_NOTIFY_SERVICE_HANDLE = 0x00000001
)

func RegisterDeviceNotification(hReceiver windows.Handle) (DeviceNotification, error) {
	hidGuid, err := getHidGuid()
	if err != nil {
		return 0, err
	}

	notificationFilter := new(_DEV_BROADCAST_DEVICEINTERFACE_W)
	notificationFilter.Size = uint32(unsafe.Sizeof(*notificationFilter))
	notificationFilter.Devicetype = _DBT_DEVTYP_DEVICEINTERFACE
	notificationFilter.Classguid = _GUID(*hidGuid)

	return registerDeviceNotification(hReceiver, notificationFilter, _DEVICE_NOTIFY_SERVICE_HANDLE)
}

func UnregisterDeviceNotification(notification DeviceNotification) error {
	r1, _, err := procUnregisterDeviceNotification.Call(uintptr(notification))
	if r1 == 0 {
		return err
	}
	return nil
}
