package serialization

import (
	"encoding/gob"
	"io"
)

// Gob is a type that wraps gob.Decoder and gob.Encoder to provide encoding and decoding functionalities.
type Gob struct {
	dec *gob.Decoder
	enc *gob.Encoder
}

// Decode decodes a value from the underlying gob.Decoder into the provided variable v.
func (g *Gob) Decode(v any) error {
	return g.dec.Decode(v)
}

// Encode serializes the input value v using gob encoding and returns an error if the encoding process fails.
func (g *Gob) Encode(v any) error {
	return g.enc.Encode(v)
}

// GobDecoder returns a Decoder that reads and decodes GOB-encoded data from the provided io.Reader.
func GobDecoder(r io.Reader) Decoder {
	return &Gob{dec: gob.NewDecoder(r)}
}

// GobEncoder returns an Encoder that writes and encodes data into GOB format using the provided io.Writer.
func GobEncoder(w io.Writer) Encoder {
	return &Gob{enc: gob.NewEncoder(w)}
}
