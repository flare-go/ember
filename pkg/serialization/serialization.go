package serialization

const (

	// JSONType represents the serialization type for JSON format.
	JSONType = "json"

	// GobType represents the serialization type for Gob format.
	GobType = "gob"
	// 其他類型
)

// Decoder and Encoder are the interface for serialization.
type Decoder interface {
	Decode(v any) error
}

// Encoder and Decoder are the interface for serialization.
type Encoder interface {
	Encode(v any) error
}
