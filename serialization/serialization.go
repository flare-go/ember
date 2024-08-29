package serialization

const (
	JsonType = "json"
	GobType  = "gob"
)

type Decoder interface {
	Decode(v any) error
}

type Encoder interface {
	Encode(v any) error
}
