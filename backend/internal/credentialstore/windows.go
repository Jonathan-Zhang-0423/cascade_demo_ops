//go:build windows

package credentialstore

import (
	"errors"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	credentialTypeGeneric  = 1
	credentialPersistLocal = 2
)

type windowsCredential struct {
	Flags              uint32
	Type               uint32
	TargetName         *uint16
	Comment            *uint16
	LastWritten        windows.Filetime
	CredentialBlobSize uint32
	CredentialBlob     *byte
	Persist            uint32
	AttributeCount     uint32
	Attributes         uintptr
	TargetAlias        *uint16
	UserName           *uint16
}

var (
	advapi32    = windows.NewLazySystemDLL("advapi32.dll")
	credWriteW  = advapi32.NewProc("CredWriteW")
	credReadW   = advapi32.NewProc("CredReadW")
	credDeleteW = advapi32.NewProc("CredDeleteW")
	credFree    = advapi32.NewProc("CredFree")
)

func storeSecret(target, username string, secret []byte) error {
	targetName, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	userName, err := windows.UTF16PtrFromString(username)
	if err != nil {
		return err
	}
	credential := windowsCredential{
		Type:               credentialTypeGeneric,
		TargetName:         targetName,
		CredentialBlobSize: uint32(len(secret)),
		Persist:            credentialPersistLocal,
		UserName:           userName,
	}
	if len(secret) > 0 {
		credential.CredentialBlob = &secret[0]
	}
	result, _, callErr := credWriteW.Call(uintptr(unsafe.Pointer(&credential)), 0)
	if result == 0 {
		return windowsCallError(callErr)
	}
	return nil
}

func readSecret(target string) ([]byte, error) {
	targetName, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return nil, err
	}
	var credential *windowsCredential
	result, _, callErr := credReadW.Call(
		uintptr(unsafe.Pointer(targetName)), credentialTypeGeneric, 0, uintptr(unsafe.Pointer(&credential)),
	)
	if result == 0 {
		return nil, windowsCallError(callErr)
	}
	defer credFree.Call(uintptr(unsafe.Pointer(credential)))
	if credential == nil || credential.CredentialBlobSize == 0 {
		return nil, errors.New("credential is empty")
	}
	return append([]byte(nil), unsafe.Slice(credential.CredentialBlob, credential.CredentialBlobSize)...), nil
}

func deleteSecret(target string) error {
	targetName, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	result, _, callErr := credDeleteW.Call(uintptr(unsafe.Pointer(targetName)), credentialTypeGeneric, 0)
	if result == 0 && !errors.Is(callErr, syscall.Errno(1168)) {
		return windowsCallError(callErr)
	}
	return nil
}

func windowsCallError(err error) error {
	if err == nil || errors.Is(err, syscall.Errno(0)) {
		return errors.New("Windows Credential Manager operation failed")
	}
	return err
}
