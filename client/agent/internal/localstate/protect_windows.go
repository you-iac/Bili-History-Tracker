// protect_windows.go 使用用户级 DPAPI；不启用 LOCAL_MACHINE，不访问浏览器凭据库。
package localstate

import (
	"errors"
	"runtime"
	"syscall"
	"unsafe"
)

const persistentCookie = true

var crypt32 = syscall.NewLazyDLL("crypt32.dll")
var encrypt = crypt32.NewProc("CryptProtectData")
var decrypt = crypt32.NewProc("CryptUnprotectData")

type blob struct {
	size uint32
	data *byte
}

func protect(data []byte) ([]byte, error)   { return transform(data, encrypt) }
func unprotect(data []byte) ([]byte, error) { return transform(data, decrypt) }
func transform(data []byte, proc *syscall.LazyProc) ([]byte, error) {
	if len(data) == 0 {
		return nil, errors.New("empty protected value")
	}
	input := blob{size: uint32(len(data)), data: &data[0]}
	var output blob
	ok, _, err := proc.Call(uintptr(unsafe.Pointer(&input)), 0, 0, 0, 0, 1, uintptr(unsafe.Pointer(&output)))
	runtime.KeepAlive(data)
	if ok == 0 {
		return nil, err
	}
	defer syscall.LocalFree(syscall.Handle(unsafe.Pointer(output.data)))
	if output.data == nil || output.size == 0 {
		return nil, errors.New("empty DPAPI response")
	}
	return append([]byte(nil), unsafe.Slice(output.data, int(output.size))...), nil
}
