package serialization

import (
	"encoding/gob"
	"io"
)

type Gob struct {
	dec *gob.Decoder
	enc *gob.Encoder
}

func (g *Gob) Decode(v any) error {
	return g.dec.Decode(v)
}

func (g *Gob) Encode(v any) error {
	return g.enc.Encode(v)
}

func GobDecoder(r io.Reader) Decoder {
	return &Gob{dec: gob.NewDecoder(r)}
}

func GobEncoder(w io.Writer) Encoder {
	return &Gob{enc: gob.NewEncoder(w)}
}
