package serialization

const (
	JsonType = "json"
	GobType  = "gob"
	// 其他類型
)

type Decoder interface {
	Decode(v any) error
}

type Encoder interface {
	Encode(v any) error
}
