package gguf_parser

import (
	"context"
	"testing"

	"github.com/davecgh/go-spew/spew"
)

func TestGGUFFile_EstimateLLaMACppRun(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name  string
		given *GGUFFile
	}{
		{
			name: "mixtral 7B",
			given: func() *GGUFFile {
				f, err := ParseGGUFFileFromHuggingFace(
					ctx,
					"NousResearch/Hermes-2-Pro-Mistral-7B-GGUF",
					"Hermes-2-Pro-Mistral-7B.Q5_K_M.gguf",
					SkipLargeMetadata())
				if err != nil {
					t.Fatal(err)
				}
				return f
			}(),
		},
		{
			name: "mixtral 8x7B",
			given: func() *GGUFFile {
				f, err := ParseGGUFFileFromHuggingFace(
					ctx,
					"NousResearch/Nous-Hermes-2-Mixtral-8x7B-DPO-GGUF",
					"Nous-Hermes-2-Mixtral-8x7B-DPO.Q5_K_M.gguf",
					SkipLargeMetadata())
				if err != nil {
					t.Fatal(err)
				}
				return f
			}(),
		},
		{
			name: "wizardlm 8x22B",
			given: func() *GGUFFile {
				f, err := ParseGGUFFileFromHuggingFace(
					ctx,
					"MaziyarPanahi/WizardLM-2-8x22B-GGUF",
					"WizardLM-2-8x22B.IQ1_M.gguf",
					SkipLargeMetadata())
				if err != nil {
					t.Fatal(err)
				}
				return f
			}(),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := tc.given
			t.Log("\n", spew.Sdump(f.EstimateLLaMACppRun()), "\n")
		})
	}
}

func TestGGUFFile_EstimateLLaMACppRun_ContextSize(t *testing.T) {
	ctx := context.Background()

	f, err := ParseGGUFFileFromHuggingFace(
		ctx,
		"NousResearch/Hermes-2-Pro-Mistral-7B-GGUF",
		"Hermes-2-Pro-Mistral-7B.Q5_K_M.gguf",
		SkipLargeMetadata())
	if err != nil {
		t.Fatal(err)
		return
	}

	cases := []struct {
		name string
		opts []GGUFRunEstimateOption
	}{
		{"1024(fp16)", []GGUFRunEstimateOption{WithLLaMACppContextSize(1024)}},
		{"1024(fp32)", []GGUFRunEstimateOption{WithLLaMACppContextSize(1024), WithLLaMACppCacheKeyType(GGMLTypeF32), WithLLaMACppCacheValueType(GGMLTypeF32)}},
		{"4096(fp16)", []GGUFRunEstimateOption{WithLLaMACppContextSize(4096)}},
		{"4096(fp32)", []GGUFRunEstimateOption{WithLLaMACppContextSize(4096), WithLLaMACppCacheKeyType(GGMLTypeF32), WithLLaMACppCacheValueType(GGMLTypeF32)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Log("\n", spew.Sdump(f.EstimateLLaMACppRun(tc.opts...)), "\n")
		})
	}
}

func TestGGUFFile_EstimateLLaMACppRun_OffloadLayers(t *testing.T) {
	ctx := context.Background()

	f, err := ParseGGUFFileFromHuggingFace(
		ctx,
		"NousResearch/Hermes-2-Pro-Mistral-7B-GGUF",
		"Hermes-2-Pro-Mistral-7B.Q5_K_M.gguf",
		SkipLargeMetadata())
	if err != nil {
		t.Fatal(err)
		return
	}

	cases := []struct {
		name string
		opts []GGUFRunEstimateOption
	}{
		{"offload 0 layer", []GGUFRunEstimateOption{WithLLaMACppOffloadLayers(0)}},
		{"offload 1 layer", []GGUFRunEstimateOption{WithLLaMACppOffloadLayers(1)}},
		{"offload 10 layers", []GGUFRunEstimateOption{WithLLaMACppOffloadLayers(10)}},
		{"offload all layers", []GGUFRunEstimateOption{}},
		{"offload 33 layers", []GGUFRunEstimateOption{WithLLaMACppOffloadLayers(33)}}, // exceeds the number of layers
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Log("\n", spew.Sdump(f.EstimateLLaMACppRun(tc.opts...)), "\n")
		})
	}
}
