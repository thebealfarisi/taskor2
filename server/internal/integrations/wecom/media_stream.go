package wecom

// media_stream.go — an attachment that never has to fit in memory.
//
// The buffered path in media_download.go holds each attachment twice: the
// whole ciphertext from the download, then the whole plaintext from the
// decrypt, both live at once while the upload runs. The engine caps concurrent
// media resolutions at eight, and the resource cap is 100 MiB, so the worst
// case a perfectly ordinary workspace can reach — four people sending two
// large files each — is eight times two hundred megabytes of live heap. On a
// self-hosted box that is an OOM, and the process it kills is serving Lark,
// Slack and DingTalk as well. That path remains only as the fallback for a
// storage backend without UploadStream; both shipped backends have one, so
// this file is what a real deployment runs.
//
// One temp file instead, and the ciphertext never reaches disk at all: it
// streams from the socket, decrypts block by block into the file, and the
// upload reads back from there. Peak heap per attachment becomes one buffer,
// and the number that would otherwise multiply is bounded by disk.
//
// The temp file is also what makes the streaming upload possible at all.
// S3Storage.UploadStream requires an exact ContentLength, and the plaintext
// length is the ciphertext length minus a pad that is only known after the
// final block is read — unknowable at the moment the upload must start. A
// file on disk has already answered the question: its size IS the length.
//
// Files are created 0600 and removed on every path. They hold decrypted
// attachment content, so their permissions are part of the feature rather
// than housekeeping.

import (
	"crypto/aes"
	"crypto/cipher"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
)

// mediaStreamChunk is how much ciphertext is decrypted at a time. Large
// enough that the syscall overhead disappears against a 100 MiB file, small
// enough that the buffers stay incidental next to everything else the process
// is holding.
const mediaStreamChunk = 256 << 10

// decryptToFile reads ciphertext from src, decrypts it into a new temp file,
// and returns the file positioned at its start along with the plaintext
// length. The caller closes and removes it.
//
// CBC needs the tail before the head can be trusted — the pad is on the last
// block — so the final block is held back until the source is exhausted and
// unpadded then. Everything before it is written as it goes.
func decryptToFile(aesKey string, src io.Reader, dir string) (f *os.File, size int64, err error) {
	mode, err := initMediaCipher(aesKey)
	if err != nil {
		return nil, 0, err
	}
	out, err := createDecryptedTempFile(dir)
	if err != nil {
		return nil, 0, err
	}
	defer func() {
		if err != nil {
			out.Close()
			if runtime.GOOS == "windows" {
				_ = os.Remove(out.Name())
			}
		}
	}()

	tail, written, err := streamDecryptBlocks(mode, src, out)
	if err != nil {
		return nil, 0, err
	}
	totalSize, err := finalizeDecryptedFile(out, tail, written)
	if err != nil {
		return nil, 0, err
	}
	return out, totalSize, nil
}

func initMediaCipher(aesKey string) (cipher.BlockMode, error) {
	key, err := decodeMediaAESKey(aesKey)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("wecom: media cipher: %w", err)
	}
	// The IV is the first 16 bytes of the key, as WeCom's own libraries do.
	return cipher.NewCBCDecrypter(block, key[:aes.BlockSize]), nil
}

func createDecryptedTempFile(dir string) (*os.File, error) {
	out, err := os.CreateTemp(dir, "wecom-media-*.bin")
	if err != nil {
		return nil, fmt.Errorf("wecom: media temp file: %w", err)
	}
	if runtime.GOOS != "windows" {
		// Unlink now on POSIX: the file stays readable through the handle and disappears
		// the moment the process lets go of it, including on a crash.
		if err := os.Remove(out.Name()); err != nil {
			out.Close()
			return nil, fmt.Errorf("wecom: media temp file: %w", err)
		}
	}
	if err := out.Chmod(0o600); err != nil && !errors.Is(err, os.ErrNotExist) {
		_ = err
	}
	return out, nil
}

func streamDecryptBlocks(mode cipher.BlockMode, src io.Reader, out *os.File) ([]byte, int64, error) {
	buf := make([]byte, mediaStreamChunk)
	tail := make([]byte, 0, mediaPadBlock+aes.BlockSize)
	var carry []byte
	var written int64

	emit := func(plain []byte) error {
		tail = append(tail, plain...)
		if len(tail) <= mediaPadBlock {
			return nil
		}
		cut := len(tail) - mediaPadBlock
		if _, werr := out.Write(tail[:cut]); werr != nil {
			return werr
		}
		written += int64(cut)
		tail = append(tail[:0], tail[cut:]...)
		return nil
	}

	for {
		n, readErr := src.Read(buf)
		if n > 0 {
			carry = append(carry, buf[:n]...)
			usable := len(carry) - len(carry)%aes.BlockSize
			if usable > 0 {
				chunk := carry[:usable]
				mode.CryptBlocks(chunk, chunk)
				if err := emit(chunk); err != nil {
					return nil, 0, fmt.Errorf("wecom: media decrypt: write: %w", err)
				}
				carry = append(carry[:0], carry[usable:]...)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, 0, fmt.Errorf("wecom: media decrypt: read: %w", readErr)
		}
	}

	if len(carry) != 0 {
		return nil, 0, fmt.Errorf("wecom: media ciphertext is not a multiple of the block size (%d trailing bytes)", len(carry))
	}
	if written == 0 && len(tail) == 0 {
		return nil, 0, errors.New("wecom: media ciphertext is empty")
	}
	return tail, written, nil
}

func finalizeDecryptedFile(out *os.File, tail []byte, written int64) (int64, error) {
	unpadded, err := unpadMedia(tail)
	if err != nil {
		return 0, err
	}
	if len(unpadded) > 0 {
		if _, err = out.Write(unpadded); err != nil {
			return 0, fmt.Errorf("wecom: media decrypt: write: %w", err)
		}
		written += int64(len(unpadded))
	}
	if _, err = out.Seek(0, io.SeekStart); err != nil {
		return 0, fmt.Errorf("wecom: media decrypt: rewind: %w", err)
	}
	return written, nil
}

// peekFile reads up to n bytes from the head of f and rewinds it, so a caller
// that needs to sniff a content type does not have to hold the whole file.
func peekFile(f *os.File, n int) ([]byte, error) {
	head := make([]byte, n)
	read, err := io.ReadFull(f, head)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	return head[:read], nil
}
