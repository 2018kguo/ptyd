package native

import (
	"bytes"
	"encoding/binary"
	"reflect"
	"testing"
)

func TestDarwinArgvStopsBeforeEnvironment(t *testing.T) {
	raw := binary.NativeEndian.AppendUint32(nil, 3)
	raw = append(raw, []byte("/path with spaces/claude\x00\x00\x00claude\x00--model\x00sonnet\x00SECRET=not-an-argument\x00")...)
	got, err := darwinArgv(raw)
	if err != nil || !reflect.DeepEqual(got, []string{"claude", "--model", "sonnet"}) {
		t.Fatalf("%q %v", got, err)
	}
	for _, b := range [][]byte{{}, raw[:3], bytes.Repeat([]byte{255}, 4), raw[:len(raw)-len("SECRET=not-an-argument\x00")-1]} {
		if _, err := darwinArgv(b); err == nil {
			t.Fatal("truncated argv accepted")
		}
	}
}
func TestDarwinPathsKeepWhitespace(t *testing.T) {
	dirs, ttys := darwinProcessPaths([]byte("p12\x00\nfcwd\x00n/path with spaces/and\nnewline\x00\nf0\x00n/dev/ttys004\x00\np13\x00\nfcwd\x00n/other\x00"))
	if dirs[12] != "/path with spaces/and\nnewline" || ttys[12] != "/dev/ttys004" || dirs[13] != "/other" {
		t.Fatalf("%v %v", dirs, ttys)
	}
}
