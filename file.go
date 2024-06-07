package gguf_parser

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/dustin/go-humanize"
	"golang.org/x/exp/constraints"

	"github.com/thxcode/gguf-parser-go/util/bytex"
	"github.com/thxcode/gguf-parser-go/util/funcx"
	"github.com/thxcode/gguf-parser-go/util/httpx"
	"github.com/thxcode/gguf-parser-go/util/osx"
)

// GGUFFile represents a GGUF file,
// see https://github.com/ggerganov/ggml/blob/master/docs/gguf.md#file-structure.
//
// Compared with the complete GGUF file,
// this structure lacks the tensor data part.
type GGUFFile struct {
	/* Basic */

	// Header is the header of the GGUF file.
	Header GGUFHeader `json:"header"`
	// TensorInfos are the tensor infos of the GGUF file,
	// the size of TensorInfos is equal to `Header.TensorCount`.
	TensorInfos GGUFTensorInfos `json:"tensorInfos"`
	// Padding is the padding size of the GGUF file,
	// which is used to split Header and TensorInfos from tensor data.
	Padding int64 `json:"padding"`
	// TensorDataStartOffset is the offset in bytes of the tensor data in this file.
	//
	// The offset is the start of the file.
	TensorDataStartOffset int64 `json:"tensorDataStartOffset"`

	/* Appendix */

	// ModelSize is the size of the model when loading.
	ModelSize GGUFBytesScalar `json:"modelSize"`
	// ModelParameters is the number of the model parameters.
	ModelParameters GGUFParametersScalar `json:"modelParameters"`
	// ModelBitsPerWeight is the bits per weight of the model,
	// which describes how many bits are used to store a weight,
	// higher is better.
	ModelBitsPerWeight GGUFBitsPerWeightScalar `json:"modelBitsPerWeight"`
}

// Types for scalar.
type (
	// GGUFBytesScalar is the scalar for bytes.
	GGUFBytesScalar uint64

	// GGUFParametersScalar is the scalar for parameters.
	GGUFParametersScalar uint64

	// GGUFBitsPerWeightScalar is the scalar for bits per weight.
	GGUFBitsPerWeightScalar float64
)

// GGUFMagic is a magic number of GGUF file,
// see https://github.com/ggerganov/ggml/blob/master/docs/gguf.md#historical-state-of-affairs.
type GGUFMagic uint32

// GGUFMagic constants.
const (
	GGUFMagicGGML   GGUFMagic = 0x67676d6c
	GGUFMagicGGMF   GGUFMagic = 0x67676d66
	GGUFMagicGGJT   GGUFMagic = 0x67676a74
	GGUFMagicGGUFLe GGUFMagic = 0x46554747 // GGUF
	GGUFMagicGGUFBe GGUFMagic = 0x47475546 // GGUF
)

// GGUFVersion is a version of GGUF file format,
// see https://github.com/ggerganov/ggml/blob/master/docs/gguf.md#version-history.
type GGUFVersion uint32

// GGUFVersion constants.
const (
	GGUFVersionV1 GGUFVersion = iota + 1
	GGUFVersionV2
	GGUFVersionV3
)

// GGUFHeader represents the header of a GGUF file.
type GGUFHeader struct {
	// Magic is a magic number that announces that this is a GGUF file.
	Magic GGUFMagic `json:"magic"`
	// Version is a version of the GGUF file format.
	Version GGUFVersion `json:"version"`
	// TensorCount is the number of tensors in the file.
	TensorCount uint64 `json:"tensorCount"`
	// MetadataKVCount is the number of key-value pairs in the metadata.
	MetadataKVCount uint64 `json:"metadataKVCount"`
	// MetadataKV are the key-value pairs in the metadata,
	MetadataKV GGUFMetadataKVs `json:"metadataKV"`
}

// GGUFMetadataValueType is a type of GGUF metadata value,
// see https://github.com/ggerganov/ggml/blob/master/docs/gguf.md#file-structure.
type GGUFMetadataValueType uint32

// GGUFMetadataValueType constants.
const (
	GGUFMetadataValueTypeUint8 GGUFMetadataValueType = iota
	GGUFMetadataValueTypeInt8
	GGUFMetadataValueTypeUint16
	GGUFMetadataValueTypeInt16
	GGUFMetadataValueTypeUint32
	GGUFMetadataValueTypeInt32
	GGUFMetadataValueTypeFloat32
	GGUFMetadataValueTypeBool
	GGUFMetadataValueTypeString
	GGUFMetadataValueTypeArray
	GGUFMetadataValueTypeUint64
	GGUFMetadataValueTypeInt64
	GGUFMetadataValueTypeFloat64
	_GGUFMetadataValueTypeCount // Unknown
)

// Types for GGUFMetadataKV.
type (
	// GGUFMetadataKV is a key-value pair in the metadata of a GGUF file.
	GGUFMetadataKV struct {
		// Key is the key of the metadata key-value pair,
		// which is no larger than 64 bytes long.
		Key string `json:"key"`
		// ValueType is the type of the metadata value.
		ValueType GGUFMetadataValueType `json:"valueType"`
		// Value is the value of the metadata key-value pair.
		Value any `json:"value"`
	}

	// GGUFMetadataKVArrayValue is a value of a GGUFMetadataKV with type GGUFMetadataValueTypeArray.
	GGUFMetadataKVArrayValue struct {
		/* Basic */

		// Type is the type of the array item.
		Type GGUFMetadataValueType `json:"type"`
		// Len is the length of the array.
		Len uint64 `json:"len"`
		// Array holds all array items.
		//
		// Array may be empty if skipping.
		Array []any `json:"array,omitempty"`

		/* Appendix */

		// StartOffset is the offset in bytes of the GGUFMetadataKVArrayValue in the GGUFFile file.
		//
		// The offset is the start of the file.
		StartOffset int64 `json:"startOffset"`

		// Size is the size of the array in bytes.
		Size int64 `json:"endOffset"`
	}

	// GGUFMetadataKVs is a list of GGUFMetadataKV.
	GGUFMetadataKVs []GGUFMetadataKV
)

// Types for GGUFTensorInfo.
type (
	// GGUFTensorInfo represents a tensor info in a GGUF file.
	GGUFTensorInfo struct {
		/* Basic */

		// Name is the name of the tensor,
		// which is no larger than 64 bytes long.
		Name string `json:"name"`
		// NDimensions is the number of dimensions of the tensor.
		NDimensions uint32 `json:"nDimensions"`
		// Dimensions is the dimensions of the tensor,
		// the length is NDimensions.
		Dimensions []uint64 `json:"dimensions"`
		// Type is the type of the tensor.
		Type GGMLType `json:"type"`
		// Offset is the offset in bytes of the tensor's data in this file.
		//
		// The offset is relative to tensor data, not to the start of the file.
		Offset uint64 `json:"offset"`

		/* Appendix */

		// StartOffset is the offset in bytes of the GGUFTensorInfo in the GGUFFile file.
		//
		// The offset is the start of the file.
		StartOffset int64 `json:"startOffset"`
	}

	// GGUFTensorInfos is a list of GGUFTensorInfo.
	GGUFTensorInfos []GGUFTensorInfo
)

var ErrGGUFFileInvalidFormat = errors.New("invalid GGUF format")

// ParseGGUFFile parses a GGUF file from the local given path,
// and returns the GGUFFile, or an error if any.
func ParseGGUFFile(path string, opts ...GGUFReadOption) (*GGUFFile, error) {
	var o _GGUFReadOptions
	for _, opt := range opts {
		opt(&o)
	}

	var (
		f io.ReadSeeker
		s int64
	)
	if o.MMap {
		mf, err := osx.OpenMmapFile(path)
		if err != nil {
			return nil, fmt.Errorf("open mmap file: %w", err)
		}
		defer osx.Close(mf)
		f = io.NewSectionReader(mf, 0, mf.Len())
		s = mf.Len()
	} else {
		ff, err := osx.Open(path)
		if err != nil {
			return nil, fmt.Errorf("open file: %w", err)
		}
		defer osx.Close(ff)
		f = ff
		s = funcx.MustNoError(ff.Stat()).Size()
	}

	return parseGGUFFile(s, f, o)
}

// ParseGGUFFileRemote parses a GGUF file from a remote URL,
// and returns a GGUFFile, or an error if any.
func ParseGGUFFileRemote(ctx context.Context, url string, opts ...GGUFReadOption) (*GGUFFile, error) {
	var o _GGUFReadOptions
	for _, opt := range opts {
		opt(&o)
	}

	cli := httpx.Client(
		httpx.ClientOptions().
			WithUserAgent("gguf-parser-go").
			If(o.Debug, func(x *httpx.ClientOption) *httpx.ClientOption {
				return x.WithDebug()
			}).
			WithTimeout(0).
			WithTransport(
				httpx.TransportOptions().
					WithoutKeepalive().
					TimeoutForDial(5*time.Second).
					TimeoutForTLSHandshake(5*time.Second).
					TimeoutForResponseHeader(5*time.Second).
					If(o.SkipProxy, func(x *httpx.TransportOption) *httpx.TransportOption {
						return x.WithoutProxy()
					}).
					If(o.ProxyURL != nil, func(x *httpx.TransportOption) *httpx.TransportOption {
						return x.WithProxy(http.ProxyURL(o.ProxyURL))
					}).
					If(o.SkipTLSVerification, func(x *httpx.TransportOption) *httpx.TransportOption {
						return x.WithoutInsecureVerify()
					})))

	var (
		f io.ReadSeeker
		s int64
	)
	{
		req, err := httpx.NewGetRequestWithContext(ctx, url)
		if err != nil {
			return nil, fmt.Errorf("new request: %w", err)
		}

		var sf *httpx.SeekerFile
		if o.BufferSize > 0 {
			sf, err = httpx.OpenSeekerFileWithSize(cli, req, o.BufferSize, 0)
		} else {
			sf, err = httpx.OpenSeekerFile(cli, req)
		}
		if err != nil {
			return nil, fmt.Errorf("open http file: %w", err)
		}
		defer osx.Close(sf)
		f = io.NewSectionReader(sf, 0, sf.Len())
		s = sf.Len()
	}

	return parseGGUFFile(s, f, o)
}

// ParseGGUFFileFromHuggingFace parses a GGUF file from Hugging Face,
// and returns a GGUFFile, or an error if any.
func ParseGGUFFileFromHuggingFace(ctx context.Context, repo, model string, opts ...GGUFReadOption) (*GGUFFile, error) {
	return ParseGGUFFileRemote(ctx, fmt.Sprintf("https://huggingface.co/%s/resolve/main/%s", repo, model), opts...)
}

func parseGGUFFile(s int64, f io.ReadSeeker, o _GGUFReadOptions) (_ *GGUFFile, err error) {
	var gf GGUFFile
	var bo binary.ByteOrder = binary.LittleEndian

	// magic
	if err = binary.Read(f, bo, &gf.Header.Magic); err != nil {
		return nil, fmt.Errorf("read magic: %w", err)
	}
	switch gf.Header.Magic {
	default:
		return nil, ErrGGUFFileInvalidFormat
	case GGUFMagicGGML, GGUFMagicGGMF, GGUFMagicGGJT:
		return nil, fmt.Errorf("unsupported format: %s", gf.Header.Magic)
	case GGUFMagicGGUFLe:
	case GGUFMagicGGUFBe:
		bo = binary.BigEndian
	}

	// version
	if err = binary.Read(f, bo, &gf.Header.Version); err != nil {
		return nil, fmt.Errorf("read version: %w", err)
	}

	rd := _GGUFReader{v: gf.Header.Version, o: o, f: f, bo: bo}

	// tensor count
	if gf.Header.Version <= GGUFVersionV1 {
		gf.Header.TensorCount, err = rd.ReadUint64FromUint32()
	} else {
		gf.Header.TensorCount, err = rd.ReadUint64()
	}
	if err != nil {
		return nil, fmt.Errorf("read tensor count: %w", err)
	}

	// metadata kv count
	if gf.Header.Version <= GGUFVersionV1 {
		gf.Header.MetadataKVCount, err = rd.ReadUint64FromUint32()
	} else {
		gf.Header.MetadataKVCount, err = rd.ReadUint64()
	}
	if err != nil {
		return nil, fmt.Errorf("read metadata kv count: %w", err)
	}

	// metadata kv
	{
		rd := _GGUFMetadataReader{_GGUFReader: rd}
		kvs := make(GGUFMetadataKVs, gf.Header.MetadataKVCount)
		for i := uint64(0); i < gf.Header.MetadataKVCount; i++ {
			kvs[i], err = rd.Read()
			if err != nil {
				return nil, fmt.Errorf("read metadata kv %d: %w", i, err)
			}
		}
		gf.Header.MetadataKV = kvs
	}

	// tensor infos
	{
		rd := _GGUFTensorInfoReader{_GGUFReader: rd}
		tis := make(GGUFTensorInfos, gf.Header.TensorCount)
		for i := uint64(0); i < gf.Header.TensorCount; i++ {
			tis[i], err = rd.Read()
			if err != nil {
				return nil, fmt.Errorf("read tensor info %d: %w", i, err)
			}
		}
		gf.TensorInfos = tis
	}

	pds, err := f.Seek(0, io.SeekCurrent)
	if err != nil {
		return nil, fmt.Errorf("seek padding start: %w", err)
	}

	// padding
	{
		// The global alignment to use, as described above.
		// This can vary to allow for different alignment schemes, but it must be a multiple of 8.
		// Some writers may not write the alignment.
		// If the alignment is not specified, assume it is 32.
		var ag uint32 = 32
		if v, ok := gf.Header.MetadataKV.Get("general.alignment"); ok {
			ag = v.ValueUint32()
		}
		gf.Padding = int64(ag) - (pds % int64(ag))
	}

	// tensor data offset
	gf.TensorDataStartOffset = pds + gf.Padding

	// model size
	gf.ModelSize = GGUFBytesScalar(s - gf.TensorDataStartOffset)

	// model parameters
	gf.ModelParameters = GGUFParametersScalar(gf.TensorInfos.Elements())

	// bpw
	if gf.ModelParameters != 0 {
		gf.ModelBitsPerWeight = GGUFBitsPerWeightScalar(float64(gf.ModelSize) * 8 / float64(gf.ModelParameters))
	}

	return &gf, nil
}

// Types for GGUF hierarchical tensors.
type (
	// IGGUFTensorInfos is an interface for GGUF tensor infos,
	// which includes basic operations.
	IGGUFTensorInfos interface {
		// Get returns the GGUFTensorInfo with the given name,
		// and true if found, and false otherwise.
		Get(name string) (info GGUFTensorInfo, found bool)
		// Search returns a list of GGUFTensorInfo with the names that match the given regex.
		Search(nameRegex *regexp.Regexp) (infos []GGUFTensorInfo)
		// Index returns a map value to the GGUFTensorInfo with the given names,
		// and the number of names found.
		Index(names []string) (infos map[string]GGUFTensorInfo, found int)
		// Elements returns the number of elements(parameters).
		Elements() uint64
		// Bytes returns the number of bytes.
		Bytes() uint64
		// Count returns the number of tensors.
		Count() uint64
	}

	// GGUFLayerTensorInfos represents hierarchical tensor infos of a GGUF file,
	// it can save GGUFNamedTensorInfos, GGUFTensorInfos, and GGUFTensorInfo.
	GGUFLayerTensorInfos []IGGUFTensorInfos

	// GGUFNamedTensorInfos is the namespace for relevant tensors,
	// which must has a name.
	GGUFNamedTensorInfos struct {
		// Name is the name of the namespace.
		Name string `json:"name"`
		// GGUFLayerTensorInfos can save GGUFNamedTensorInfos, GGUFTensorInfos, or GGUFTensorInfo.
		//
		// If the item is type of GGUFTensorInfo, it must be the leaf node.
		//
		// Any branch nodes are type of GGUFNamedTensorInfos or GGUFTensorInfos,
		// which can be nested.
		//
		// Branch nodes store in type pointer.
		GGUFLayerTensorInfos `json:"items,omitempty"`
	}
)

// Layers converts the GGUFTensorInfos to GGUFLayerTensorInfos.
func (gf *GGUFFile) Layers(ignores ...string) GGUFLayerTensorInfos {
	ls := gf.layers()
	if len(ignores) != 0 {
		_, ls, _ = ls.Cut(ignores)
		return ls
	}
	return ls
}

func (gf *GGUFFile) layers() GGUFLayerTensorInfos {
	var ret GGUFLayerTensorInfos

	pm := make(map[string]any)
	for i := range gf.TensorInfos {
		ps := strings.Split(gf.TensorInfos[i].Name, ".")
		switch {
		default:
			ret = append(ret, gf.TensorInfos[i])
			continue
		case len(ps) >= 2 && ps[0] == "blk":
			p := strings.Join([]string{ps[0], ps[1]}, ".")
			if _, ok := pm[p]; !ok {
				l := &GGUFNamedTensorInfos{Name: p}
				pm[p] = l
				ret = append(ret, l)
			}
			l := pm[p].(*GGUFNamedTensorInfos)
			l.GGUFLayerTensorInfos = append(l.GGUFLayerTensorInfos, gf.TensorInfos[i])
		case len(ps) >= 3 && (ps[0] == "decoder" || ps[0] == "encoder"):
			p := ps[0]
			if _, ok := pm[p]; !ok {
				xl := &GGUFNamedTensorInfos{Name: p}
				pm[p] = xl
				ret = append(ret, xl)
			}
			xl := pm[p].(*GGUFNamedTensorInfos)
			if ps[1] != "block" {
				xl.GGUFLayerTensorInfos = append(xl.GGUFLayerTensorInfos, gf.TensorInfos[i])
				continue
			}
			p = strings.Join([]string{ps[0], ps[1], ps[2]}, ".")
			if _, ok := pm[p]; !ok {
				l := &GGUFNamedTensorInfos{Name: p}
				pm[p] = l
				xl.GGUFLayerTensorInfos = append(xl.GGUFLayerTensorInfos, l)
			}
			l := pm[p].(*GGUFNamedTensorInfos)
			l.GGUFLayerTensorInfos = append(l.GGUFLayerTensorInfos, gf.TensorInfos[i])
		}
	}
	return ret
}

func (s GGUFBytesScalar) String() string {
	if s == 0 {
		return "0 B"
	}
	return humanize.IBytes(uint64(s))
}

func (s GGUFParametersScalar) String() string {
	if s == 0 {
		return "0"
	}
	switch {
	case s >= 1e15:
		return humanize.CommafWithDigits(float64(s)/1e15, 1) + " Q"
	case s >= 1e12:
		return humanize.CommafWithDigits(float64(s)/1e12, 1) + " T"
	case s >= 1e9:
		return humanize.CommafWithDigits(float64(s)/1e9, 1) + " B"
	case s >= 1e6:
		return humanize.CommafWithDigits(float64(s)/1e6, 1) + " M"
	case s >= 1e3:
		return humanize.CommafWithDigits(float64(s)/1e3, 1) + " K"
	default:
		return strconv.Itoa(int(s))
	}
}

func (s GGUFBitsPerWeightScalar) String() string {
	if s == 0 {
		return "0 bpw"
	}
	return strconv.FormatFloat(float64(s), 'f', 2, 64) + " bpw"
}

func (kv GGUFMetadataKV) ValueUint8() uint8 {
	if kv.ValueType != GGUFMetadataValueTypeUint8 {
		panic(fmt.Errorf("invalid type: %v", kv.ValueType))
	}
	return kv.Value.(uint8)
}

func (kv GGUFMetadataKV) ValueInt8() int8 {
	if kv.ValueType != GGUFMetadataValueTypeInt8 {
		panic(fmt.Errorf("invalid type: %v", kv.ValueType))
	}
	return kv.Value.(int8)
}

func (kv GGUFMetadataKV) ValueUint16() uint16 {
	if kv.ValueType != GGUFMetadataValueTypeUint16 {
		panic(fmt.Errorf("invalid type: %v", kv.ValueType))
	}
	return kv.Value.(uint16)
}

func (kv GGUFMetadataKV) ValueInt16() int16 {
	if kv.ValueType != GGUFMetadataValueTypeInt16 {
		panic(fmt.Errorf("invalid type: %v", kv.ValueType))
	}
	return kv.Value.(int16)
}

func (kv GGUFMetadataKV) ValueUint32() uint32 {
	if kv.ValueType != GGUFMetadataValueTypeUint32 {
		panic(fmt.Errorf("invalid type: %v", kv.ValueType))
	}
	return kv.Value.(uint32)
}

func (kv GGUFMetadataKV) ValueInt32() int32 {
	if kv.ValueType != GGUFMetadataValueTypeInt32 {
		panic(fmt.Errorf("invalid type: %v", kv.ValueType))
	}
	return kv.Value.(int32)
}

func (kv GGUFMetadataKV) ValueFloat32() float32 {
	if kv.ValueType != GGUFMetadataValueTypeFloat32 {
		panic(fmt.Errorf("invalid type: %v", kv.ValueType))
	}
	return kv.Value.(float32)
}

func (kv GGUFMetadataKV) ValueBool() bool {
	if kv.ValueType != GGUFMetadataValueTypeBool {
		panic(fmt.Errorf("invalid type: %v", kv.ValueType))
	}
	return kv.Value.(bool)
}

func (kv GGUFMetadataKV) ValueString() string {
	if kv.ValueType != GGUFMetadataValueTypeString {
		panic(fmt.Errorf("invalid type: %v", kv.ValueType))
	}
	return kv.Value.(string)
}

func (kv GGUFMetadataKV) ValueArray() GGUFMetadataKVArrayValue {
	if kv.ValueType != GGUFMetadataValueTypeArray {
		panic(fmt.Errorf("invalid type: %v", kv.ValueType))
	}
	return kv.Value.(GGUFMetadataKVArrayValue)
}

func (kv GGUFMetadataKV) ValueUint64() uint64 {
	if kv.ValueType != GGUFMetadataValueTypeUint64 {
		panic(fmt.Errorf("invalid type: %v", kv.ValueType))
	}
	return kv.Value.(uint64)
}

func (kv GGUFMetadataKV) ValueInt64() int64 {
	if kv.ValueType != GGUFMetadataValueTypeInt64 {
		panic(fmt.Errorf("invalid type: %v", kv.ValueType))
	}
	return kv.Value.(int64)
}

func (kv GGUFMetadataKV) ValueFloat64() float64 {
	if kv.ValueType != GGUFMetadataValueTypeFloat64 {
		panic(fmt.Errorf("invalid type: %v", kv.ValueType))
	}
	return kv.Value.(float64)
}

// ValueNumeric returns the numeric values of the GGUFMetadataKV,
// and panics if the value type is not numeric.
//
// ValueNumeric is a generic function, and the type T must be constraints.Integer or constraints.Float.
//
// Compare to the GGUFMetadataKV's Value* functions,
// ValueNumeric will cast the original value to the target type.
func ValueNumeric[T constraints.Integer | constraints.Float](kv GGUFMetadataKV) T {
	switch kv.ValueType {
	case GGUFMetadataValueTypeUint8:
		return T(kv.Value.(uint8))
	case GGUFMetadataValueTypeInt8:
		return T(kv.Value.(int8))
	case GGUFMetadataValueTypeUint16:
		return T(kv.Value.(int16))
	case GGUFMetadataValueTypeInt16:
		return T(kv.Value.(int16))
	case GGUFMetadataValueTypeUint32:
		return T(kv.Value.(uint32))
	case GGUFMetadataValueTypeInt32:
		return T(kv.Value.(int32))
	case GGUFMetadataValueTypeFloat32:
		return T(kv.Value.(float32))
	case GGUFMetadataValueTypeUint64:
		return T(kv.Value.(uint64))
	case GGUFMetadataValueTypeInt64:
		return T(kv.Value.(int64))
	case GGUFMetadataValueTypeFloat64:
		return T(kv.Value.(float64))
	default:
	}
	panic(fmt.Errorf("invalid type: %v", kv.ValueType))
}

func (av GGUFMetadataKVArrayValue) ValuesUint8() []uint8 {
	if av.Type != GGUFMetadataValueTypeUint8 {
		panic(fmt.Errorf("invalid type: %v", av.Type))
	}
	v := make([]uint8, av.Len)
	for i := uint64(0); i < av.Len; i++ {
		v[i] = av.Array[i].(uint8)
	}
	return v
}

func (av GGUFMetadataKVArrayValue) ValuesInt8() []int8 {
	if av.Type != GGUFMetadataValueTypeInt8 {
		panic(fmt.Errorf("invalid type: %v", av.Type))
	}
	v := make([]int8, av.Len)
	for i := uint64(0); i < av.Len; i++ {
		v[i] = av.Array[i].(int8)
	}
	return v
}

func (av GGUFMetadataKVArrayValue) ValuesUint16() []uint16 {
	if av.Type != GGUFMetadataValueTypeUint16 {
		panic(fmt.Errorf("invalid type: %v", av.Type))
	}
	v := make([]uint16, av.Len)
	for i := uint64(0); i < av.Len; i++ {
		v[i] = av.Array[i].(uint16)
	}
	return v
}

func (av GGUFMetadataKVArrayValue) ValuesInt16() []int16 {
	if av.Type != GGUFMetadataValueTypeInt16 {
		panic(fmt.Errorf("invalid type: %v", av.Type))
	}
	v := make([]int16, av.Len)
	for i := uint64(0); i < av.Len; i++ {
		v[i] = av.Array[i].(int16)
	}
	return v
}

func (av GGUFMetadataKVArrayValue) ValuesUint32() []uint32 {
	if av.Type != GGUFMetadataValueTypeUint32 {
		panic(fmt.Errorf("invalid type: %v", av.Type))
	}
	v := make([]uint32, av.Len)
	for i := uint64(0); i < av.Len; i++ {
		v[i] = av.Array[i].(uint32)
	}
	return v
}

func (av GGUFMetadataKVArrayValue) ValuesInt32() []int32 {
	if av.Type != GGUFMetadataValueTypeInt32 {
		panic(fmt.Errorf("invalid type: %v", av.Type))
	}
	v := make([]int32, av.Len)
	for i := uint64(0); i < av.Len; i++ {
		v[i] = av.Array[i].(int32)
	}
	return v
}

func (av GGUFMetadataKVArrayValue) ValuesFloat32() []float32 {
	if av.Type != GGUFMetadataValueTypeFloat32 {
		panic(fmt.Errorf("invalid type: %v", av.Type))
	}
	v := make([]float32, av.Len)
	for i := uint64(0); i < av.Len; i++ {
		v[i] = av.Array[i].(float32)
	}
	return v
}

func (av GGUFMetadataKVArrayValue) ValuesBool() []bool {
	if av.Type != GGUFMetadataValueTypeBool {
		panic(fmt.Errorf("invalid type: %v", av.Type))
	}
	v := make([]bool, av.Len)
	for i := uint64(0); i < av.Len; i++ {
		v[i] = av.Array[i].(bool)
	}
	return v
}

func (av GGUFMetadataKVArrayValue) ValuesString() []string {
	if av.Type != GGUFMetadataValueTypeString {
		panic(fmt.Errorf("invalid type: %v", av.Type))
	}
	v := make([]string, av.Len)
	for i := uint64(0); i < av.Len; i++ {
		v[i] = av.Array[i].(string)
	}
	return v
}

func (av GGUFMetadataKVArrayValue) ValuesArray() []GGUFMetadataKVArrayValue {
	if av.Type != GGUFMetadataValueTypeArray {
		panic(fmt.Errorf("invalid type: %v", av.Type))
	}
	v := make([]GGUFMetadataKVArrayValue, av.Len)
	for i := uint64(0); i < av.Len; i++ {
		v[i] = av.Array[i].(GGUFMetadataKVArrayValue)
	}
	return v
}

func (av GGUFMetadataKVArrayValue) ValuesUint64() []uint64 {
	if av.Type != GGUFMetadataValueTypeUint64 {
		panic(fmt.Errorf("invalid type: %v", av.Type))
	}
	v := make([]uint64, av.Len)
	for i := uint64(0); i < av.Len; i++ {
		v[i] = av.Array[i].(uint64)
	}
	return v
}

func (av GGUFMetadataKVArrayValue) ValuesInt64() []int64 {
	if av.Type != GGUFMetadataValueTypeInt64 {
		panic(fmt.Errorf("invalid type: %v", av.Type))
	}
	v := make([]int64, av.Len)
	for i := uint64(0); i < av.Len; i++ {
		v[i] = av.Array[i].(int64)
	}
	return v
}

func (av GGUFMetadataKVArrayValue) ValuesFloat64() []float64 {
	if av.Type != GGUFMetadataValueTypeFloat64 {
		panic(fmt.Errorf("invalid type: %v", av.Type))
	}
	v := make([]float64, av.Len)
	for i := uint64(0); i < av.Len; i++ {
		v[i] = av.Array[i].(float64)
	}
	return v
}

// ValuesNumeric returns the numeric values of the GGUFMetadataKVArrayValue,
// and panics if the value type is not numeric.
//
// ValuesNumeric is a generic function, and the type T must be constraints.Integer or constraints.Float.
//
// Compare to the GGUFMetadataKVArrayValue's Value* functions,
// ValuesNumeric will cast the original value to the target type.
func ValuesNumeric[T constraints.Integer | constraints.Float](av GGUFMetadataKVArrayValue) []T {
	v := make([]T, av.Len)
	for i := uint64(0); i < av.Len; i++ {
		switch av.Type {
		case GGUFMetadataValueTypeUint8:
			v[i] = T(av.Array[i].(uint8))
		case GGUFMetadataValueTypeInt8:
			v[i] = T(av.Array[i].(int8))
		case GGUFMetadataValueTypeUint16:
			v[i] = T(av.Array[i].(uint16))
		case GGUFMetadataValueTypeInt16:
			v[i] = T(av.Array[i].(int16))
		case GGUFMetadataValueTypeUint32:
			v[i] = T(av.Array[i].(uint32))
		case GGUFMetadataValueTypeInt32:
			v[i] = T(av.Array[i].(int32))
		case GGUFMetadataValueTypeFloat32:
			v[i] = T(av.Array[i].(float32))
		case GGUFMetadataValueTypeUint64:
			v[i] = T(av.Array[i].(uint64))
		case GGUFMetadataValueTypeInt64:
			v[i] = T(av.Array[i].(int64))
		case GGUFMetadataValueTypeFloat64:
			v[i] = T(av.Array[i].(float64))
		default:
			panic(fmt.Errorf("invalid type: %v", av.Type))
		}
	}
	return v
}

// Get returns the GGUFMetadataKV with the given key,
// and true if found, and false otherwise.
func (kvs GGUFMetadataKVs) Get(key string) (value GGUFMetadataKV, found bool) {
	for i := range kvs {
		if kvs[i].Key == key {
			return kvs[i], true
		}
	}
	return GGUFMetadataKV{}, false
}

// Search returns a list of GGUFMetadataKV with the keys that match the given regex.
func (kvs GGUFMetadataKVs) Search(keyRegex *regexp.Regexp) (values []GGUFMetadataKV) {
	for i := range kvs {
		if keyRegex.MatchString(kvs[i].Key) {
			values = append(values, kvs[i])
		}
	}
	return values
}

// Index returns a map value to the GGUFMetadataKVs with the given keys,
// and the number of keys found.
func (kvs GGUFMetadataKVs) Index(keys []string) (values map[string]GGUFMetadataKV, found int) {
	ks := make(map[string]struct{}, len(keys))
	for i := range keys {
		ks[keys[i]] = struct{}{}
	}
	values = make(map[string]GGUFMetadataKV)
	for i := range kvs {
		if _, ok := ks[kvs[i].Key]; ok {
			values[kvs[i].Key] = kvs[i]
			found++
		}
		if found == len(ks) {
			break
		}
	}
	return values, found
}

// Get returns the GGUFTensorInfo with the given name,
// and true if found, and false otherwise.
func (ti GGUFTensorInfo) Get(name string) (info GGUFTensorInfo, found bool) {
	if ti.Name == name {
		return ti, true
	}
	return GGUFTensorInfo{}, false
}

// Search returns a list of GGUFTensorInfo with the names that match the given regex.
func (ti GGUFTensorInfo) Search(nameRegex *regexp.Regexp) (infos []GGUFTensorInfo) {
	if nameRegex.MatchString(ti.Name) {
		return []GGUFTensorInfo{ti}
	}
	return nil
}

// Index returns a map value to the GGUFTensorInfo with the given names,
// and the number of names found.
func (ti GGUFTensorInfo) Index(names []string) (infos map[string]GGUFTensorInfo, found int) {
	if len(names) == 0 {
		return nil, 0
	}
	if names[0] == ti.Name {
		return map[string]GGUFTensorInfo{ti.Name: ti}, 1
	}
	return nil, 0
}

// Elements returns the number of elements of the GGUFTensorInfo,
// which is inspired by
// https://github.com/ggerganov/ggml/blob/a10a8b880c059b3b29356eb9a9f8df72f03cdb6a/src/ggml.c#L2597-L2601.
func (ti GGUFTensorInfo) Elements() uint64 {
	if ti.NDimensions == 0 {
		return 0
	}

	ret := uint64(1)
	for i := uint32(0); i < ti.NDimensions; i++ {
		ret *= ti.Dimensions[i]
	}
	return ret
}

// Bytes returns the number of bytes of the GGUFTensorInfo,
// which is inspired by
// https://github.com/ggerganov/ggml/blob/a10a8b880c059b3b29356eb9a9f8df72f03cdb6a/src/ggml.c#L2609-L2626.
func (ti GGUFTensorInfo) Bytes() uint64 {
	if ti.NDimensions == 0 {
		return 0
	}

	tt, ok := ti.Type.Trait()
	if !ok {
		panic(fmt.Errorf("invalid type: %v", ti.Type))
	}

	// https://github.com/ggerganov/ggml/blob/a10a8b880c059b3b29356eb9a9f8df72f03cdb6a/src/ggml.c#L3210-L3214
	nb := make([]uint64, 0, ti.NDimensions)
	{
		nb = append(nb, tt.TypeSize)
		nb = append(nb, nb[0]*(ti.Dimensions[0]/tt.BlockSize))
		for i := uint32(2); i < ti.NDimensions; i++ {
			nb = append(nb, nb[i-1]*ti.Dimensions[i-1])
		}
	}

	var ret uint64
	if tt.BlockSize == 1 {
		ret = tt.TypeSize
		for i := uint32(0); i < ti.NDimensions; i++ {
			ret += (ti.Dimensions[i] - 1) * nb[i]
		}
		return ret
	}

	ret = ti.Dimensions[0] * nb[0] / tt.BlockSize
	for i := uint32(1); i < ti.NDimensions; i++ {
		ret += (ti.Dimensions[i] - 1) * nb[i]
	}
	return ret
}

// Count returns the number of GGUF tensors of the GGUFTensorInfo,
// which is always 1.
func (ti GGUFTensorInfo) Count() uint64 {
	return 1
}

// Get returns the GGUFTensorInfo with the given name,
// and true if found, and false otherwise.
func (tis GGUFTensorInfos) Get(name string) (info GGUFTensorInfo, found bool) {
	for i := range tis {
		if tis[i].Name == name {
			return tis[i], true
		}
	}
	return GGUFTensorInfo{}, false
}

// Search returns a list of GGUFTensorInfo with the names that match the given regex.
func (tis GGUFTensorInfos) Search(nameRegex *regexp.Regexp) (infos []GGUFTensorInfo) {
	for i := range tis {
		if nameRegex.MatchString(tis[i].Name) {
			infos = append(infos, tis[i])
		}
	}
	return infos
}

// Index returns a map value to the GGUFTensorInfos with the given names,
// and the number of names found.
func (tis GGUFTensorInfos) Index(names []string) (infos map[string]GGUFTensorInfo, found int) {
	ns := make(map[string]struct{}, len(names))
	for i := range names {
		ns[names[i]] = struct{}{}
	}
	infos = make(map[string]GGUFTensorInfo)
	for i := range tis {
		if _, ok := ns[tis[i].Name]; ok {
			infos[tis[i].Name] = tis[i]
			found++
		}
		if found == len(ns) {
			break
		}
	}
	return infos, found
}

// Elements returns the number of elements of the GGUFTensorInfos.
func (tis GGUFTensorInfos) Elements() uint64 {
	var ret uint64
	for i := range tis {
		ret += tis[i].Elements()
	}
	return ret
}

// Bytes returns the number of bytes of the GGUFTensorInfos.
func (tis GGUFTensorInfos) Bytes() uint64 {
	var ret uint64
	for i := range tis {
		ret += tis[i].Bytes()
	}
	return ret
}

// Count returns the number of GGUF tensors of the GGUFTensorInfos.
func (tis GGUFTensorInfos) Count() uint64 {
	return uint64(len(tis))
}

// Get returns the IGGUFTensorInfos with the given name,
// and true if found, and false otherwise.
func (ltis GGUFLayerTensorInfos) Get(name string) (info GGUFTensorInfo, found bool) {
	for i := range ltis {
		switch v := ltis[i].(type) {
		case GGUFTensorInfo:
			if v.Name == name {
				return v, true
			}
		case *GGUFNamedTensorInfos:
			info, found = v.GGUFLayerTensorInfos.Get(name)
			if found {
				return info, true
			}
		}
	}
	return GGUFTensorInfo{}, false
}

// Search returns a list of GGUFTensorInfo with the names that match the given regex.
func (ltis GGUFLayerTensorInfos) Search(nameRegex *regexp.Regexp) (infos []GGUFTensorInfo) {
	for i := range ltis {
		switch v := ltis[i].(type) {
		case GGUFTensorInfo:
			if nameRegex.MatchString(v.Name) {
				infos = append(infos, v)
			}
		case *GGUFNamedTensorInfos:
			infos = append(infos, v.Search(nameRegex)...)
		}
	}
	return infos
}

// Index returns a map value to the GGUFTensorInfos with the given names,
// and the number of names found.
func (ltis GGUFLayerTensorInfos) Index(names []string) (infos map[string]GGUFTensorInfo, found int) {
	ns := make(map[string]struct{}, len(names))
	for i := range names {
		ns[names[i]] = struct{}{}
	}
	infos = make(map[string]GGUFTensorInfo)
	for i := range ltis {
		switch v := ltis[i].(type) {
		case GGUFTensorInfo:
			if _, ok := ns[v.Name]; ok {
				infos[v.Name] = v
				found++
			}
		case *GGUFNamedTensorInfos:
			inf, _ := v.Index(names)
			for k := range inf {
				infos[k] = inf[k]
				found++
			}
		}
		if found == len(ns) {
			break
		}
	}
	return infos, found
}

// Elements returns the number of elements of the GGUFLayerTensorInfos.
func (ltis GGUFLayerTensorInfos) Elements() uint64 {
	var ret uint64
	for i := range ltis {
		ret += ltis[i].Elements()
	}
	return ret
}

// Bytes returns the number of bytes of the GGUFLayerTensorInfos.
func (ltis GGUFLayerTensorInfos) Bytes() uint64 {
	var ret uint64
	for i := range ltis {
		ret += ltis[i].Bytes()
	}
	return ret
}

// Count returns the number of GGUF tensors of the GGUFLayerTensorInfos.
func (ltis GGUFLayerTensorInfos) Count() uint64 {
	var ret uint64
	for i := range ltis {
		ret += ltis[i].Count()
	}
	return ret
}

// Cut splits the GGUFLayerTensorInfos into two parts,
// and returns the GGUFLayerTensorInfos with the names that match the given names at first,
// and the GGUFLayerTensorInfos without the names at second,
// and true if the GGUFLayerTensorInfos with the names are found, and false otherwise.
func (ltis GGUFLayerTensorInfos) Cut(names []string) (before, after GGUFLayerTensorInfos, found bool) {
	ns := make(map[string]struct{}, len(names))
	for i := range names {
		ns[names[i]] = struct{}{}
	}
	before = make(GGUFLayerTensorInfos, 0, len(names))
	after = make(GGUFLayerTensorInfos, 0, len(ltis))

	for i := range ltis {
		switch v := ltis[i].(type) {
		case GGUFTensorInfo:
			if _, ok := ns[v.Name]; ok {
				before = append(before, v)
				continue
			}
			after = append(after, v)
		case *GGUFNamedTensorInfos:
			if _, ok := ns[v.Name]; ok {
				before = append(before, v)
				continue
			}
			after = append(after, v)
		}
	}
	return before, after, len(before) > 0
}

type _GGUFReader struct {
	v  GGUFVersion
	o  _GGUFReadOptions
	f  io.ReadSeeker
	bo binary.ByteOrder
}

func (rd _GGUFReader) ReadUint8() (v uint8, err error) {
	err = binary.Read(rd.f, rd.bo, &v)
	if err != nil {
		return 0, fmt.Errorf("read uint8: %w", err)
	}
	return v, nil
}

func (rd _GGUFReader) ReadInt8() (v int8, err error) {
	err = binary.Read(rd.f, rd.bo, &v)
	if err != nil {
		return 0, fmt.Errorf("read int8: %w", err)
	}
	return v, nil
}

func (rd _GGUFReader) ReadUint16() (v uint16, err error) {
	err = binary.Read(rd.f, rd.bo, &v)
	if err != nil {
		return 0, fmt.Errorf("read uint16: %w", err)
	}
	return v, nil
}

func (rd _GGUFReader) ReadInt16() (v int16, err error) {
	err = binary.Read(rd.f, rd.bo, &v)
	if err != nil {
		return 0, fmt.Errorf("read int16: %w", err)
	}
	return v, nil
}

func (rd _GGUFReader) ReadUint32() (v uint32, err error) {
	err = binary.Read(rd.f, rd.bo, &v)
	if err != nil {
		return 0, fmt.Errorf("read uint32: %w", err)
	}
	return v, nil
}

func (rd _GGUFReader) ReadUint64FromUint32() (uint64, error) {
	v, err := rd.ReadUint32()
	return uint64(v), err
}

func (rd _GGUFReader) ReadInt32() (v int32, err error) {
	err = binary.Read(rd.f, rd.bo, &v)
	if err != nil {
		return 0, fmt.Errorf("read int32: %w", err)
	}
	return v, nil
}

func (rd _GGUFReader) ReadFloat32() (v float32, err error) {
	err = binary.Read(rd.f, rd.bo, &v)
	if err != nil {
		return 0, fmt.Errorf("read float32: %w", err)
	}
	return v, nil
}

func (rd _GGUFReader) ReadBool() (v bool, err error) {
	b, err := rd.ReadUint8()
	if err != nil {
		return false, fmt.Errorf("read bool: %w", err)
	}
	return b != 0, nil
}

func (rd _GGUFReader) ReadString() (v string, err error) {
	var l uint64
	if rd.v <= GGUFVersionV1 {
		l, err = rd.ReadUint64FromUint32()
	} else {
		l, err = rd.ReadUint64()
	}
	if err != nil {
		return "", fmt.Errorf("read string length: %w", err)
	}

	b := bytex.GetBytes(l)
	defer bytex.Put(b)
	if _, err = rd.f.Read(b); err != nil {
		return "", fmt.Errorf("read string: %w", err)
	}

	return string(bytes.TrimSpace(b)), nil
}

func (rd _GGUFReader) SkipReadingString() (err error) {
	var l uint64
	if rd.v <= GGUFVersionV1 {
		l, err = rd.ReadUint64FromUint32()
	} else {
		l, err = rd.ReadUint64()
	}
	if err != nil {
		return fmt.Errorf("read string length: %w", err)
	}
	_, err = rd.f.Seek(int64(l), io.SeekCurrent)
	if err != nil {
		return fmt.Errorf("seek string: %w", err)
	}
	return nil
}

func (rd _GGUFReader) ReadArray() (v GGUFMetadataKVArrayValue, err error) {
	v.StartOffset, err = rd.f.Seek(0, io.SeekCurrent)
	if err != nil {
		return v, fmt.Errorf("read array start: %w", err)
	}

	if err = binary.Read(rd.f, rd.bo, &v.Type); err != nil {
		return v, fmt.Errorf("read array item type: %w", err)
	}

	if rd.v <= GGUFVersionV1 {
		v.Len, err = rd.ReadUint64FromUint32()
	} else {
		v.Len, err = rd.ReadUint64()
	}
	if err != nil {
		return v, fmt.Errorf("read array length: %w", err)
	}

	itemStart, err := rd.f.Seek(0, io.SeekCurrent)
	if err != nil {
		return v, fmt.Errorf("seek array item start: %w", err)
	}

	if !rd.o.SkipLargeMetadata {
		v.Array = make([]any, v.Len)
		for i := uint64(0); i < v.Len; i++ {
			v.Array[i], err = rd.ReadValue(v.Type)
			if err != nil {
				return v, fmt.Errorf("read array item %d: %w", i, err)
			}
		}

		itemEnd, err := rd.f.Seek(0, io.SeekCurrent)
		if err != nil {
			return v, fmt.Errorf("seek array item end: %w", err)
		}
		v.Size = itemEnd - itemStart

		return v, nil
	}

	switch v.Type {
	case GGUFMetadataValueTypeUint8, GGUFMetadataValueTypeInt8, GGUFMetadataValueTypeBool:
		_, err = rd.f.Seek(int64(v.Len), io.SeekCurrent)
	case GGUFMetadataValueTypeUint16, GGUFMetadataValueTypeInt16:
		_, err = rd.f.Seek(int64(v.Len)*2, io.SeekCurrent)
	case GGUFMetadataValueTypeUint32, GGUFMetadataValueTypeInt32, GGUFMetadataValueTypeFloat32:
		_, err = rd.f.Seek(int64(v.Len)*4, io.SeekCurrent)
	case GGUFMetadataValueTypeUint64, GGUFMetadataValueTypeInt64, GGUFMetadataValueTypeFloat64:
		_, err = rd.f.Seek(int64(v.Len)*8, io.SeekCurrent)
	case GGUFMetadataValueTypeString:
		for i := uint64(0); i < v.Len; i++ {
			if err = rd.SkipReadingString(); err != nil {
				return v, fmt.Errorf("seek array[string] %d: %w", i, err)
			}
		}
	default:
		// Should not happen.
		panic(fmt.Errorf("invalid type: %v", v.Type))
	}
	if err != nil {
		return v, fmt.Errorf("seek array end: %w", err)
	}

	itemEnd, err := rd.f.Seek(0, io.SeekCurrent)
	if err != nil {
		return v, fmt.Errorf("seek array item end: %w", err)
	}
	v.Size = itemEnd - itemStart

	return v, nil
}

func (rd _GGUFReader) ReadUint64() (v uint64, err error) {
	err = binary.Read(rd.f, rd.bo, &v)
	if err != nil {
		return 0, fmt.Errorf("read uint64: %w", err)
	}
	return v, nil
}

func (rd _GGUFReader) ReadInt64() (v int64, err error) {
	err = binary.Read(rd.f, rd.bo, &v)
	if err != nil {
		return 0, fmt.Errorf("read int64: %w", err)
	}
	return v, nil
}

func (rd _GGUFReader) ReadFloat64() (v float64, err error) {
	err = binary.Read(rd.f, rd.bo, &v)
	if err != nil {
		return 0, fmt.Errorf("read float64: %w", err)
	}
	return v, nil
}

func (rd _GGUFReader) ReadValue(vt GGUFMetadataValueType) (v any, err error) {
	if vt >= _GGUFMetadataValueTypeCount {
		return nil, fmt.Errorf("invalid type: %v", vt)
	}

	switch vt {
	case GGUFMetadataValueTypeUint8:
		v, err = rd.ReadUint8()
	case GGUFMetadataValueTypeInt8:
		v, err = rd.ReadInt8()
	case GGUFMetadataValueTypeUint16:
		v, err = rd.ReadUint16()
	case GGUFMetadataValueTypeInt16:
		v, err = rd.ReadInt16()
	case GGUFMetadataValueTypeUint32:
		v, err = rd.ReadUint32()
	case GGUFMetadataValueTypeInt32:
		v, err = rd.ReadInt32()
	case GGUFMetadataValueTypeFloat32:
		v, err = rd.ReadFloat32()
	case GGUFMetadataValueTypeBool:
		v, err = rd.ReadBool()
	case GGUFMetadataValueTypeString:
		v, err = rd.ReadString()
	case GGUFMetadataValueTypeArray:
		v, err = rd.ReadArray()
	case GGUFMetadataValueTypeUint64:
		v, err = rd.ReadUint64()
	case GGUFMetadataValueTypeInt64:
		v, err = rd.ReadInt64()
	case GGUFMetadataValueTypeFloat64:
		v, err = rd.ReadFloat64()
	default:
		// Should not happen.
		panic(fmt.Errorf("invalid type: %v", vt))
	}
	if err != nil {
		return nil, err
	}
	return v, nil
}

type _GGUFMetadataReader struct {
	_GGUFReader
}

func (rd _GGUFMetadataReader) Read() (kv GGUFMetadataKV, err error) {
	kv.Key, err = rd.ReadString()
	if err != nil {
		return kv, fmt.Errorf("read key: %w", err)
	}

	{
		vt, err := rd.ReadUint32()
		if err != nil {
			return kv, fmt.Errorf("read value type: %w", err)
		}
		kv.ValueType = GGUFMetadataValueType(vt)
		if kv.ValueType >= _GGUFMetadataValueTypeCount {
			return kv, fmt.Errorf("invalid value type: %v", kv.ValueType)
		}
	}

	kv.Value, err = rd.ReadValue(kv.ValueType)
	if err != nil {
		return kv, fmt.Errorf("read %s value: %w", kv.Key, err)
	}

	return kv, nil
}

type _GGUFTensorInfoReader struct {
	_GGUFReader
}

func (rd _GGUFTensorInfoReader) Read() (ti GGUFTensorInfo, err error) {
	ti.StartOffset, err = rd.f.Seek(0, io.SeekCurrent)
	if err != nil {
		return ti, fmt.Errorf("seek tensor info start: %w", err)
	}

	ti.Name, err = rd.ReadString()
	if err != nil {
		return ti, fmt.Errorf("read name: %w", err)
	}

	ti.NDimensions, err = rd.ReadUint32()
	if err != nil {
		return ti, fmt.Errorf("read n dimensions: %w", err)
	}

	ti.Dimensions = make([]uint64, ti.NDimensions)
	for i := uint32(0); i < ti.NDimensions; i++ {
		if rd.v <= GGUFVersionV1 {
			ti.Dimensions[i], err = rd.ReadUint64FromUint32()
		} else {
			ti.Dimensions[i], err = rd.ReadUint64()
		}
		if err != nil {
			return ti, fmt.Errorf("read dimension %d: %w", i, err)
		}
	}

	{
		v, err := rd.ReadUint32()
		if err != nil {
			return ti, fmt.Errorf("read type: %w", err)
		}
		ti.Type = GGMLType(v)
		if ti.Type >= _GGMLTypeCount {
			return ti, fmt.Errorf("invalid type: %v", ti.Type)
		}
	}

	ti.Offset, err = rd.ReadUint64()
	if err != nil {
		return ti, fmt.Errorf("read offset: %w", err)
	}

	return ti, nil
}
