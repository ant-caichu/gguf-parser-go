// Code generated by "stringer -linecomment -type GGUFMagic -output zz_generated.ggufmagic.stringer.go -trimprefix GGUFMagic"; DO NOT EDIT.

package gguf_parser

import "strconv"

func _() {
	// An "invalid array index" compiler error signifies that the constant values have changed.
	// Re-run the stringer command to generate them again.
	var x [1]struct{}
	_ = x[GGUFMagicGGML-1734831468]
	_ = x[GGUFMagicGGMF-1734831462]
	_ = x[GGUFMagicGGJT-1734830708]
	_ = x[GGUFMagicGGUFLe-1179993927]
	_ = x[GGUFMagicGGUFBe-1195857222]
}

const (
	_GGUFMagic_name_0 = "GGUF"
	_GGUFMagic_name_1 = "GGUF"
	_GGUFMagic_name_2 = "GGJT"
	_GGUFMagic_name_3 = "GGMF"
	_GGUFMagic_name_4 = "GGML"
)

func (i GGUFMagic) String() string {
	switch {
	case i == 1179993927:
		return _GGUFMagic_name_0
	case i == 1195857222:
		return _GGUFMagic_name_1
	case i == 1734830708:
		return _GGUFMagic_name_2
	case i == 1734831462:
		return _GGUFMagic_name_3
	case i == 1734831468:
		return _GGUFMagic_name_4
	default:
		return "GGUFMagic(" + strconv.FormatInt(int64(i), 10) + ")"
	}
}
