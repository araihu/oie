// Package securearchive stores versioned Oiê data in passphrase-encrypted age files.
package securearchive

import (
	"errors"
	"io"

	"filippo.io/age"
)

type Kind string

const (
	Identity Kind = "OIE OMEMO IDENTITY 1\n"
	History  Kind = "OIE OMEMO HISTORY 1\n"
)

func Seal(dst io.Writer, kind Kind, password string, payload func(io.Writer) error) error {
	recipient, err := age.NewScryptRecipient(password)
	if err != nil {
		return err
	}
	writer, err := age.Encrypt(dst, recipient)
	if err != nil {
		return err
	}
	if _, err = io.WriteString(writer, string(kind)); err != nil {
		return err
	}
	if err = payload(writer); err != nil {
		return err
	}
	return writer.Close()
}

// Open callers must read to EOF before applying any changes. This validates
// the final age chunk and detects truncated or extended archives.
func Open(src io.Reader, kind Kind, password string) (io.Reader, error) {
	identity, err := age.NewScryptIdentity(password)
	if err != nil {
		return nil, err
	}
	identity.SetMaxWorkFactor(18)
	reader, err := age.Decrypt(src, identity)
	if err != nil {
		return nil, err
	}
	header := make([]byte, len(kind))
	if _, err = io.ReadFull(reader, header); err != nil {
		return nil, err
	}
	if string(header) != string(kind) {
		return nil, errors.New("unexpected Oiê archive type or version")
	}
	return reader, nil
}
