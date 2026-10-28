package content

import "testing"

func TestCheckLinks(t *testing.T) {
	for _, ok := range []string{"/", "/collections", "/collections/mirrors?filter=shape:arch-crown", "https://www.instagram.com/pinnacleverve/", "mailto:care@example.com", "tel:+91 80840 47949"} {
		if err := CheckLinks(ok); err != nil {
			t.Errorf("CheckLinks(%q) = %v, want ok", ok, err)
		}
	}
	for _, bad := range []string{"javascript:alert(1)", "//evil.example", "/\\evil.example", "http://insecure.example", "data:text/html,x", "collections", "https://", "/a b", " /x"} {
		if err := CheckLinks(bad); err == nil {
			t.Errorf("CheckLinks(%q) accepted", bad)
		}
	}
}
