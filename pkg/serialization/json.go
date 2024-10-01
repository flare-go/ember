package serialization

import (
	"encoding/json"
	"io"
)

// JSON provides encoding and decoding functionalities for  data.
type JSON struct {
	dec *json.Decoder
	enc *json.Encoder
}

// Decode parses the encoded data and stores the result in the value pointed to by v.
func (j *JSON) Decode(v any) error {
	return j.dec.Decode(v)
}

// Encode serializes the given value v into and writes it to the output stream of the encoder.
func (j *JSON) Encode(v any) error {
	return j.enc.Encode(v)
}

// JSONDecoder returns a Decoder that reads and decodes data from the provided io.Reader.
func JSONDecoder(r io.Reader) Decoder {
	return &JSON{dec: json.NewDecoder(r)}
}

// JSONEncoder returns an Encoder that encodes data into format using the provided io.Writer.
func JSONEncoder(w io.Writer) Encoder {
	return &JSON{enc: json.NewEncoder(w)}
}
