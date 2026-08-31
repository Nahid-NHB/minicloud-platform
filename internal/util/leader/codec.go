package leader

import "encoding/json"

func mustMarshal(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func mustUnmarshal(data []byte, v any) {
	_ = json.Unmarshal(data, v)
}