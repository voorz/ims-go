package media

func NewNullCodec(name string, sampleRate int) *NullCodec {
	return &NullCodec{name: name, sampleRate: sampleRate}
}

func (c *NullCodec) Name() string       { return c.name }
func (c *NullCodec) SampleRate() int    { return c.sampleRate }
func (c *NullCodec) FrameDuration() int { return 20 }
func (c *NullCodec) Encode(pcm []int16) ([]byte, error) {
	out := make([]byte, len(pcm)*2)
	for i, s := range pcm {
		out[i*2] = byte(s)
		out[i*2+1] = byte(s >> 8)
	}
	return out, nil
}
func (c *NullCodec) Decode(data []byte) ([]int16, error) {
	if len(data)%2 != 0 {
		data = data[:len(data)-1]
	}
	out := make([]int16, len(data)/2)
	for i := range out {
		out[i] = int16(data[i*2]) | int16(data[i*2+1])<<8
	}
	return out, nil
}
