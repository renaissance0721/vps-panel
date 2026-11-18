package main

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

type profile struct {
	Panel      string `json:"panel"`
	Credential string `json:"credential"`
}

func profilePath() (string, error) {
	root, err := windows.KnownFolderPath(windows.FOLDERID_LocalAppData, 0)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "VPSPanelLocalConnector", "pairing.dpapi"), nil
}

func protectProfile(data []byte, decrypt bool) ([]byte, error) {
	if len(data) == 0 || len(data) > 16<<10 {
		return nil, errors.New("invalid local connector profile")
	}
	input := windows.DataBlob{Size: uint32(len(data)), Data: &data[0]}
	var output windows.DataBlob
	var err error
	if decrypt {
		err = windows.CryptUnprotectData(&input, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &output)
	} else {
		err = windows.CryptProtectData(&input, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &output)
	}
	if err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(uintptr(unsafe.Pointer(output.Data))))
	buffer := unsafe.Slice(output.Data, int(output.Size))
	defer clear(buffer)
	return append([]byte(nil), buffer...), nil
}

func saveProfile(value profile) error {
	path, err := profilePath()
	if err != nil {
		return err
	}
	plaintext, err := json.Marshal(value)
	if err != nil {
		return err
	}
	defer clear(plaintext)
	ciphertext, err := protectProfile(plaintext, false)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, "pairing-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, writeErr := file.Write(ciphertext)
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

func loadProfile() (profile, error) {
	var value profile
	path, err := profilePath()
	if err != nil {
		return value, err
	}
	file, err := os.Open(path)
	if err != nil {
		return value, err
	}
	defer file.Close()
	ciphertext, err := io.ReadAll(io.LimitReader(file, (16<<10)+1))
	if err != nil {
		return value, err
	}
	plaintext, err := protectProfile(ciphertext, true)
	if err != nil {
		return value, err
	}
	defer clear(plaintext)
	err = json.Unmarshal(plaintext, &value)
	return value, err
}
