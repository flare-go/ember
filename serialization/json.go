package serialization

import (
	"encoding/json"
	"io"
)

type Json struct {
	dec *json.Decoder
	enc *json.Encoder
}

func (j *Json) Decode(v any) error {
	return j.dec.Decode(v)
}

func (j *Json) Encode(v any) error {
	return j.enc.Encode(v)
}

func JsonDecoder(r io.Reader) Decoder {
	return &Json{dec: json.NewDecoder(r)}
}

func JsonEncoder(w io.Writer) Encoder {
	return &Json{enc: json.NewEncoder(w)}
}
