// Package gguf 只读解析 GGUF **文件头部**（不加载权重），用于：
//   - 校验文件确实是可加载的 GGUF；
//   - 为推荐引擎提供 n_layer / n_embd / n_head / n_ctx 等结构参数；
//   - 识别量化类型与参数量级。
//
// 参考 GGUF v2/v3 规范：文件头 + 元数据 KV 区 + 张量信息区 + 数据区。
package gguf

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Meta 从 GGUF 头部提取的关键信息。
type Meta struct {
	Path        string  `json:"path"`
	SizeBytes   int64   `json:"size_bytes"`
	SizeMB      int     `json:"size_mb"`
	Arch        string  `json:"arch"`
	Name        string  `json:"name,omitempty"`
	Layers      int     `json:"layers"`
	EmbDim      int     `json:"emb_dim"`
	Heads       int     `json:"heads"`
	HeadsKV     int     `json:"heads_kv"`
	NativeCtx   int     `json:"native_ctx"`
	Quant       string  `json:"quant"`
	FileType    int     `json:"file_type"`
	ParamsB     float64 `json:"params_b"` // 参数量（十亿），可由文件大小与量化推算
	ExpertCount int     `json:"expert_count,omitempty"`
	MoE         bool    `json:"moe"`
}

// KVBytesPerElement 返回量化类型对应的每元素字节数（近似值，用于 KV 显存估算）。
func KVBytesPerElement(t string) float64 {
	switch strings.ToLower(t) {
	case "f32":
		return 4
	case "f16", "bf16":
		return 2
	case "q8_0":
		return 1.0625
	case "q5_1":
		return 0.6875
	case "q5_0":
		return 0.625
	case "q4_1":
		return 0.5625
	case "q4_0", "iq4_nl":
		return 0.5625
	}
	return 2
}

// BytesOfQuant 同上，供推荐引擎调用。
func BytesOfQuant(t string) float64 { return KVBytesPerElement(t) }

const (
	ggufMagic = "GGUF"
	// 防御性上限：避免损坏文件导致超大分配
	maxArrayElements = 4_000_000
)

var errNotGGUF = errors.New("不是 GGUF 文件（魔数不匹配）")

// Validate 只校验魔数与版本，不读元数据（用于入库前快速校验）。
func Validate(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	var head [8]byte
	if _, err := io.ReadFull(f, head[:]); err != nil {
		return fmt.Errorf("文件过短，不是有效的 GGUF")
	}
	if string(head[:4]) != ggufMagic {
		return errNotGGUF
	}
	ver := binary.LittleEndian.Uint32(head[4:])
	if ver < 1 || ver > 3 {
		return fmt.Errorf("GGUF 版本 %d 不受支持", ver)
	}
	return nil
}

// Parse 解析 GGUF 头部，返回结构参数。
func Parse(path string) (*Meta, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	r := newReader(f)
	magic, err := r.bytes(4)
	if err != nil {
		return nil, err
	}
	if string(magic) != ggufMagic {
		return nil, errNotGGUF
	}
	version, err := r.u32()
	if err != nil {
		return nil, err
	}
	if version < 1 || version > 3 {
		return nil, fmt.Errorf("GGUF 版本 %d 不受支持", version)
	}
	tensorCount, err := r.u64()
	if err != nil {
		return nil, err
	}
	kvCount, err := r.u64()
	if err != nil {
		return nil, err
	}

	m := &Meta{Path: path, SizeBytes: st.Size(), SizeMB: int(st.Size() / (1024 * 1024))}
	raw := map[string]any{}
	for i := uint64(0); i < kvCount; i++ {
		key, err := r.str()
		if err != nil {
			break
		}
		val, err := r.value()
		if err != nil {
			break
		}
		if !isBigArray(val) {
			raw[key] = val
		}
	}
	m.Arch = str(raw, "general.architecture")
	if m.Arch == "" {
		m.Arch = "llama"
	}
	m.Name = str(raw, "general.name")
	m.Layers = intOf(raw, m.Arch+".block_count")
	m.EmbDim = intOf(raw, m.Arch+".embedding_length")
	m.Heads = intOf(raw, m.Arch+".attention.head_count")
	m.HeadsKV = intOf(raw, m.Arch+".attention.head_count_kv")
	m.NativeCtx = intOf(raw, m.Arch+".context_length")
	if m.NativeCtx == 0 {
		if kv := intOf(raw, m.Arch+".attention.key_length"); kv > 0 {
			_ = kv
		}
	}
	m.ExpertCount = intOf(raw, m.Arch+".expert_count")
	m.MoE = m.ExpertCount > 0
	m.FileType = intOf(raw, "general.file_type")

	// 兜底：元数据缺失时从文件名推断。
	base := strings.ToLower(filepath.Base(path))
	if m.HeadsKV == 0 {
		m.HeadsKV = m.Heads
	}
	if m.NativeCtx == 0 {
		m.NativeCtx = 4096
	}
	m.Quant = QuantName(m.FileType)
	if m.Quant == "" {
		m.Quant = quantFromName(base)
	}
	m.ParamsB = estimateParamsB(m, st.Size())

	tensors := tensorCount
	_ = tensors
	return m, nil
}

// QuantName 把 general.file_type 映射为量化名。
func QuantName(ft int) string {
	switch ft {
	case 0:
		return "F32"
	case 1:
		return "F16"
	case 2:
		return "Q4_0"
	case 3:
		return "Q4_1"
	case 7:
		return "Q8_0"
	case 8:
		return "Q5_0"
	case 9:
		return "Q5_1"
	case 10:
		return "Q2_K"
	case 11:
		return "Q3_K_S"
	case 12:
		return "Q3_K_M"
	case 13:
		return "Q3_K_L"
	case 14:
		return "Q4_K_S"
	case 15:
		return "Q4_K_M"
	case 16:
		return "Q5_K_S"
	case 17:
		return "Q5_K_M"
	case 18:
		return "Q6_K"
	case 19:
		return "IQ2_XXS"
	case 20:
		return "IQ2_XS"
	case 21:
		return "Q2_K_S"
	case 22:
		return "IQ3_XS"
	case 23:
		return "IQ3_XXS"
	case 24:
		return "IQ1_S"
	case 25:
		return "IQ4_NL"
	case 26:
		return "IQ3_S"
	case 27:
		return "IQ3_M"
	case 28:
		return "IQ2_S"
	case 29:
		return "IQ2_M"
	case 30:
		return "IQ4_XS"
	case 31:
		return "IQ1_M"
	case 32:
		return "BF16"
	}
	return ""
}

var quantRe = regexp.MustCompile(`(?i)(iq\d+_[a-z]+|q\d+_k_[sml]|q\d+_[k01]|q\d+_0|f16|bf16|f32|q8_0|q\d+_k)`)

func quantFromName(base string) string {
	if m := quantRe.FindString(base); m != "" {
		return strings.ToUpper(m)
	}
	return "未知"
}

// estimateParamsB 由文件大小与量化位数粗估参数量（十亿）。
func estimateParamsB(m *Meta, size int64) float64 {
	bpw := 4.5 // 默认按 Q4_K_M 的近似位宽
	switch {
	case strings.HasPrefix(m.Quant, "Q8"), strings.HasPrefix(m.Quant, "Q5"):
		bpw = 5.5
	case strings.HasPrefix(m.Quant, "Q6"):
		bpw = 6.6
	case strings.HasPrefix(m.Quant, "Q4"):
		bpw = 4.6
	case strings.HasPrefix(m.Quant, "Q3"):
		bpw = 3.5
	case strings.HasPrefix(m.Quant, "Q2"), strings.HasPrefix(m.Quant, "IQ1"), strings.HasPrefix(m.Quant, "IQ2"):
		bpw = 2.6
	case m.Quant == "F16", m.Quant == "BF16":
		bpw = 16
	case m.Quant == "F32":
		bpw = 32
	}
	// 参数量 ≈ 文件字节数 × 8 / bpw（未计入嵌入层重复等开销，量级足够）
	p := float64(size) * 8 / bpw / 1e9
	return math.Round(p*10) / 10
}

// --- 内部读取器 ---

type reader struct{ r io.Reader }

func newReader(f *os.File) *reader { return &reader{r: f} }

func (r *reader) bytes(n int) ([]byte, error) {
	buf := make([]byte, n)
	_, err := io.ReadFull(r.r, buf)
	return buf, err
}

func (r *reader) u32() (uint32, error) {
	b, err := r.bytes(4)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(b), nil
}

func (r *reader) u64() (uint64, error) {
	b, err := r.bytes(8)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint64(b), nil
}

func (r *reader) str() (string, error) {
	n, err := r.u64()
	if err != nil {
		return "", err
	}
	if n > 1<<20 {
		return "", fmt.Errorf("字符串长度异常: %d", n)
	}
	b, err := r.bytes(int(n))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// value 读取一个元数据值；数组会返回元素个数与首个元素（大数组不驻留内存）。
func (r *reader) value() (any, error) {
	t, err := r.u32()
	if err != nil {
		return nil, err
	}
	return r.valueOf(t)
}

// bigArray 标记被截断的大数组。
type bigArray struct{ n uint64 }

func isBigArray(v any) bool { _, ok := v.(bigArray); return ok }

func (r *reader) valueOf(t uint32) (any, error) {
	switch t {
	case 0: // uint8
		b, err := r.bytes(1)
		return uint64(b[0]), err
	case 1: // int8
		b, err := r.bytes(1)
		return int64(int8(b[0])), err
	case 2: // uint16
		b, err := r.bytes(2)
		return uint64(binary.LittleEndian.Uint16(b)), err
	case 3: // int16
		b, err := r.bytes(2)
		return int64(int16(binary.LittleEndian.Uint16(b))), err
	case 4: // uint32
		return r.u32()
	case 5: // int32
		v, err := r.u32()
		return int64(int32(v)), err
	case 6: // float32
		v, err := r.u32()
		return math.Float32frombits(v), err
	case 7: // bool
		b, err := r.bytes(1)
		return b[0] != 0, err
	case 8: // string
		return r.str()
	case 9: // array
		et, err := r.u32()
		if err != nil {
			return nil, err
		}
		n, err := r.u64()
		if err != nil {
			return nil, err
		}
		if n > maxArrayElements {
			return bigArray{n: n}, nil
		}
		if n > 4096 {
			// 大数组（如词表）逐个跳过：按元素类型定长跳过，字符串数组则逐个读长度
			if err := r.skipN(et, n); err != nil {
				return nil, err
			}
			return bigArray{n: n}, nil
		}
		out := make([]any, 0, n)
		for i := uint64(0); i < n; i++ {
			v, err := r.valueOf(et)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	}
	return nil, fmt.Errorf("未知元数据类型 %d", t)
}

func (r *reader) skipN(elemType uint32, n uint64) error {
	fixed := map[uint32]int{0: 1, 1: 1, 2: 2, 3: 2, 4: 4, 5: 4, 6: 4, 7: 1, 10: 8, 11: 8, 12: 8}
	if sz, ok := fixed[elemType]; ok {
		return r.skip(int64(sz) * int64(n))
	}
	switch elemType {
	case 8: // 字符串数组
		for i := uint64(0); i < n; i++ {
			l, err := r.u64()
			if err != nil {
				return err
			}
			if err := r.skip(int64(l)); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("不支持的数组元素类型 %d", elemType)
	}
}

func (r *reader) skip(n int64) error {
	if n <= 0 {
		return nil
	}
	switch src := r.r.(type) {
	case io.Seeker:
		_, err := src.Seek(n, io.SeekCurrent)
		return err
	default:
		_, err := io.CopyN(io.Discard, r.r, n)
		return err
	}
}

func str(m map[string]any, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func intOf(m map[string]any, key string) int {
	v, ok := m[key]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case uint64:
		return int(n)
	case int64:
		return int(n)
	case uint32:
		return int(n)
	case float32:
		return int(n)
	case string:
		i, _ := strconv.Atoi(n)
		return i
	}
	return 0
}
