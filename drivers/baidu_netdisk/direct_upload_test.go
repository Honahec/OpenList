package baidu_netdisk

import "testing"

func TestValidateBaiduDirectUploadHashes(t *testing.T) {
	valid := "d41d8cd98f00b204e9800998ecf8427e"
	if err := validateBaiduDirectUploadHashes(8, 4, []string{valid, valid}); err != nil {
		t.Fatalf("valid hashes rejected: %v", err)
	}
	for _, test := range []struct {
		name   string
		hashes []string
	}{
		{name: "wrong count", hashes: []string{valid}},
		{name: "invalid hex", hashes: []string{valid, "not-an-md5"}},
		{name: "uppercase", hashes: []string{valid, "D41D8CD98F00B204E9800998ECF8427E"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := validateBaiduDirectUploadHashes(8, 4, test.hashes); err == nil {
				t.Fatal("invalid hashes accepted")
			}
		})
	}
}

func TestEncryptBaiduDirectUploadPayload(t *testing.T) {
	key := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	token, err := encryptBaiduDirectUploadPayload(key, baiduDirectUploadPayload{
		Target: "https://d.pcs.baidu.com/rest/2.0/pcs/superfile2", Expires: 123,
	})
	if err != nil {
		t.Fatal(err)
	}
	if token == "" {
		t.Fatal("empty encrypted token")
	}
}
