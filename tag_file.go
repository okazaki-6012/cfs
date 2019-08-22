package cfs

import (
	"encoding/json"
	"io"
	"io/ioutil"
	"time"
)

type TagFile struct {
	Name       string           `json:"name"`
	CreatedAt  time.Time        `json:"createdAt"`
	EncryptKey string           `json:"encryptKey"`
	EncryptIv  string           `json:"encryptIv"`
	Attr       ContentAttribute `json:"attr"`
	Hash       string           `json:"hash"`
}

func TagFileFromReader(r io.Reader) (*TagFile, error) {
	tagBytes, err := ioutil.ReadAll(r)
	if err != nil {
		return nil, err
	}

	var tag TagFile
	err = json.Unmarshal(tagBytes, &tag)
	if err != nil {
		return nil, err
	}

	return &tag, nil
}

func TagFileFromFile(tag_filepath string) (*TagFile, error) {
	tagBytes, err := ioutil.ReadFile(tag_filepath)
	if err != nil {
		return nil, err
	}

	var tag TagFile
	err = json.Unmarshal(tagBytes, &tag)
	if err != nil {
		return nil, err
	}

	return &tag, nil
}
