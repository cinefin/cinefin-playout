//go:build windows

package hardware

import (
	"context"
	"syscall"
	"unsafe"
)

var (
	user32                   = syscall.NewLazyDLL("user32.dll")
	procEnumDisplayDevicesW  = user32.NewProc("EnumDisplayDevicesW")
	procEnumDisplaySettingsW = user32.NewProc("EnumDisplaySettingsW")
)

const (
	displayDeviceAttachedToDesktop = 0x00000001
	enumCurrentSettings            = 0xFFFFFFFF
)

type displayDeviceW struct {
	cb           uint32
	DeviceName   [32]uint16
	DeviceString [128]uint16
	StateFlags   uint32
	DeviceID     [128]uint16
	DeviceKey    [128]uint16
}

// devmodeW mirrors the Win32 DEVMODEW display fields (wingdi.h). Only the display
// members are used; the printer union members are laid out as their display
// equivalents so the struct size and field offsets match.
type devmodeW struct {
	dmDeviceName         [32]uint16
	dmSpecVersion        uint16
	dmDriverVersion      uint16
	dmSize               uint16
	dmDriverExtra        uint16
	dmFields             uint32
	dmPositionX          int32
	dmPositionY          int32
	dmDisplayOrientation uint32
	dmDisplayFixedOutput uint32
	dmColor              int16
	dmDuplex             int16
	dmYResolution        int16
	dmTTOption           int16
	dmCollate            int16
	dmFormName           [32]uint16
	dmLogPixels          uint16
	dmBitsPerPel         uint32
	dmPelsWidth          uint32
	dmPelsHeight         uint32
	dmDisplayFlags       uint32
	dmDisplayFrequency   uint32
	dmICMMethod          uint32
	dmICMIntent          uint32
	dmMediaType          uint32
	dmDitherType         uint32
	dmReserved1          uint32
	dmReserved2          uint32
	dmPanningWidth       uint32
	dmPanningHeight      uint32
}

// enumScreens lists the desktop-attached displays via the GDI display APIs. The
// mpv --screen ordinal matches the enumeration order. Best-effort: any failure
// yields an empty list rather than an error.
func enumScreens(_ context.Context) []Screen {
	var screens []Screen
	for i := uint32(0); ; i++ {
		var dd displayDeviceW
		dd.cb = uint32(unsafe.Sizeof(dd))
		r, _, _ := procEnumDisplayDevicesW.Call(0, uintptr(i), uintptr(unsafe.Pointer(&dd)), 0)
		if r == 0 {
			break // no more adapters
		}
		if dd.StateFlags&displayDeviceAttachedToDesktop == 0 {
			continue
		}
		name := syscall.UTF16ToString(dd.DeviceName[:])
		s := Screen{Index: len(screens), Name: name}
		var dm devmodeW
		dm.dmSize = uint16(unsafe.Sizeof(dm))
		ok, _, _ := procEnumDisplaySettingsW.Call(
			uintptr(unsafe.Pointer(&dd.DeviceName[0])),
			enumCurrentSettings,
			uintptr(unsafe.Pointer(&dm)),
		)
		if ok != 0 {
			s.W = int(dm.dmPelsWidth)
			s.H = int(dm.dmPelsHeight)
			s.Hz = float64(dm.dmDisplayFrequency)
		}
		screens = append(screens, s)
	}
	return screens
}
