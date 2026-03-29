//go:build ignore

package devnotify

/*
#include <windows.h>
#include <dbt.h>
*/
import "C"

type (
	_GUID                            C.GUID
	_HDEVNOTIFY                      C.HDEVNOTIFY
	_DEV_BROADCAST_HDR               C.DEV_BROADCAST_HDR
	_DEV_BROADCAST_DEVICEINTERFACE_W C.DEV_BROADCAST_DEVICEINTERFACE_W
)
