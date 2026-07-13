//go:build linux && amd64 && cgo

package wecom_chat_archive

/*
#cgo CFLAGS: -I${SRCDIR}/../../../../sdk_x86_v3_20250205/C_sdk
#cgo LDFLAGS: -L${SRCDIR}/../../../../sdk_x86_v3_20250205/C_sdk -lWeWorkFinanceSdk_C -Wl,-rpath,$ORIGIN
#include <stdlib.h>
#include "WeWorkFinanceSdk_C.h"
*/
import "C"

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"unsafe"
)

type financeSDKClient struct {
	cfg *Config
	sdk *C.WeWorkFinanceSdk_t
}

func newUnavailableClient(cfg *Config) ArchiveClient {
	return newFinanceSDKClient(cfg)
}

func newFinanceSDKClient(cfg *Config) ArchiveClient {
	return &financeSDKClient{cfg: cfg}
}

func (c *financeSDKClient) Validate(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := c.init(); err != nil {
		return err
	}
	data, err := c.getChatData(0, 1)
	if err != nil {
		return err
	}
	items, _, err := parseChatDataResponse(data)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		return nil
	}
	_, err = decryptEncryptKey(c.cfg.PrivateKey, items[0].EncryptRandomKey)
	return err
}

func (c *financeSDKClient) FetchMessages(ctx context.Context, startSeq uint64, limit int) ([]ArchiveMessageEnvelope, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if err := c.init(); err != nil {
		return nil, false, err
	}

	limit = clampSDKLimit(limit)
	data, err := c.getChatData(startSeq, limit)
	if err != nil {
		return nil, false, err
	}
	items, _, err := parseChatDataResponse(data)
	if err != nil {
		return nil, false, err
	}

	messages := make([]ArchiveMessageEnvelope, 0, len(items))
	for _, item := range items {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		encryptKey, err := decryptEncryptKey(c.cfg.PrivateKey, item.EncryptRandomKey)
		if err != nil {
			messages = append(messages, conversionErrorMessage(item.Seq, item.MsgID, sdkConversionError{Seq: item.Seq, MsgID: item.MsgID, Err: sdkDecryptKeyError{Err: err}}))
			continue
		}
		payload, err := decryptSDKData(encryptKey, item.EncryptChatMsg)
		if err != nil {
			messages = append(messages, conversionErrorMessage(item.Seq, item.MsgID, sdkConversionError{Seq: item.Seq, MsgID: item.MsgID, Err: fmt.Errorf("decrypt data: %w", err)}))
			continue
		}
		msg, err := decodeDecryptedMessage(item.Seq, item.MsgID, payload)
		if err != nil {
			messages = append(messages, conversionErrorMessage(item.Seq, item.MsgID, sdkConversionError{Seq: item.Seq, MsgID: item.MsgID, Err: err}))
			continue
		}
		messages = append(messages, msg)
	}
	if len(items) > 0 && allMessagesAreDecryptKeyErrors(messages) {
		return nil, false, fmt.Errorf("wecom sdk decrypt key failed for all messages in batch")
	}

	return messages, len(items) >= limit, nil
}

func allMessagesAreDecryptKeyErrors(messages []ArchiveMessageEnvelope) bool {
	if len(messages) == 0 {
		return false
	}
	for _, msg := range messages {
		var conversionErr sdkConversionError
		if !errors.As(msg.ConversionError, &conversionErr) {
			return false
		}
		var decryptKeyErr sdkDecryptKeyError
		if !errors.As(conversionErr.Err, &decryptKeyErr) {
			return false
		}
	}
	return true
}

func (c *financeSDKClient) Close() error {
	if c.sdk != nil {
		C.DestroySdk(c.sdk)
		c.sdk = nil
	}
	return nil
}

func (c *financeSDKClient) init() error {
	if c.sdk != nil {
		return nil
	}
	sdk := C.NewSdk()
	if sdk == nil {
		return fmt.Errorf("wecom sdk NewSdk failed")
	}

	corpID := C.CString(c.cfg.CorpID)
	secret := C.CString(c.cfg.Secret)
	defer C.free(unsafe.Pointer(corpID))
	defer C.free(unsafe.Pointer(secret))

	if code := C.Init(sdk, corpID, secret); code != 0 {
		C.DestroySdk(sdk)
		return fmt.Errorf("wecom sdk Init failed with code %d", int(code))
	}
	c.sdk = sdk
	return nil
}

func (c *financeSDKClient) getChatData(startSeq uint64, limit int) ([]byte, error) {
	proxy := C.CString(c.cfg.Settings.Proxy)
	proxyPassword := C.CString(c.cfg.Settings.ProxyPassword)
	defer C.free(unsafe.Pointer(proxy))
	defer C.free(unsafe.Pointer(proxyPassword))

	slice := C.NewSlice()
	if slice == nil {
		return nil, fmt.Errorf("wecom sdk NewSlice failed")
	}
	defer C.FreeSlice(slice)

	code := C.GetChatData(
		c.sdk,
		C.ulonglong(startSeq),
		C.uint(limit),
		proxy,
		proxyPassword,
		C.int(c.cfg.Settings.TimeoutSeconds),
		slice,
	)
	if code != 0 {
		return nil, fmt.Errorf("wecom sdk GetChatData failed with code %d", int(code))
	}
	return sliceBytes(slice), nil
}

func decryptEncryptKey(privateKeyPEM string, encryptedKey string) (string, error) {
	block, _ := pem.Decode([]byte(privateKeyPEM))
	if block == nil {
		return "", fmt.Errorf("parse private key PEM failed")
	}

	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		parsed, parseErr := x509.ParsePKCS1PrivateKey(block.Bytes)
		if parseErr != nil {
			return "", fmt.Errorf("parse private key failed")
		}
		key = parsed
	}
	privateKey, ok := key.(*rsa.PrivateKey)
	if !ok {
		return "", fmt.Errorf("private key is not RSA")
	}

	ciphertext, err := base64.StdEncoding.DecodeString(encryptedKey)
	if err != nil {
		return "", fmt.Errorf("decode encrypted key failed")
	}
	plaintext, err := rsa.DecryptPKCS1v15(rand.Reader, privateKey, ciphertext)
	if err != nil {
		return "", fmt.Errorf("RSA decrypt encrypted key failed")
	}
	return string(plaintext), nil
}

func decryptSDKData(encryptKey string, encryptMsg string) ([]byte, error) {
	key := C.CString(encryptKey)
	msg := C.CString(encryptMsg)
	defer C.free(unsafe.Pointer(key))
	defer C.free(unsafe.Pointer(msg))

	slice := C.NewSlice()
	if slice == nil {
		return nil, fmt.Errorf("wecom sdk NewSlice failed")
	}
	defer C.FreeSlice(slice)

	if code := C.DecryptData(key, msg, slice); code != 0 {
		return nil, fmt.Errorf("code %d", int(code))
	}
	return sliceBytes(slice), nil
}

func sliceBytes(slice *C.Slice_t) []byte {
	if slice == nil || slice.buf == nil || slice.len <= 0 {
		return nil
	}
	return C.GoBytes(unsafe.Pointer(slice.buf), C.int(slice.len))
}

func clampSDKLimit(limit int) int {
	if limit < 1 {
		return 1
	}
	if limit > 1000 {
		return 1000
	}
	return limit
}
