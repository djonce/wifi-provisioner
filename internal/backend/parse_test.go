package backend

import "testing"

func TestSplitTerse(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{`MyWifi:80:WPA2`, []string{"MyWifi", "80", "WPA2"}},
		{`My\:Wifi:70:--`, []string{"My:Wifi", "70", "--"}},
		{`:0:`, []string{"", "0", ""}},
	}
	for _, c := range cases {
		got := splitTerse(c.in)
		if len(got) != len(c.want) {
			t.Fatalf("%q: len %d != %d (%v)", c.in, len(got), len(c.want), got)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("%q field %d: %q != %q", c.in, i, got[i], c.want[i])
			}
		}
	}
}

func TestDbmToQuality(t *testing.T) {
	cases := map[string]int{
		"signal: -50.00 dBm":  100,
		"signal: -100.00 dBm": 0,
		"signal: -75.00 dBm":  50,
		"signal: -30.00 dBm":  100, // clamped
	}
	for in, want := range cases {
		if got := dbmToQuality(in); got != want {
			t.Fatalf("%q: got %d want %d", in, got, want)
		}
	}
}

func TestParseIWScan(t *testing.T) {
	sample := `BSS aa:bb:cc:dd:ee:01(on wlan0)
	signal: -45.00 dBm
	SSID: HomeNet
	RSN:	 * Version: 1
BSS aa:bb:cc:dd:ee:02(on wlan0)
	signal: -80.00 dBm
	SSID: OpenCafe
BSS aa:bb:cc:dd:ee:03(on wlan0)
	signal: -40.00 dBm
	SSID: HomeNet
`
	nets := parseIWScan(sample)
	if len(nets) != 2 {
		t.Fatalf("want 2 unique SSIDs, got %d (%v)", len(nets), nets)
	}
	// Sorted by signal desc; HomeNet should be first with its strongest reading.
	if nets[0].SSID != "HomeNet" {
		t.Fatalf("strongest should be HomeNet, got %q", nets[0].SSID)
	}
	if !nets[0].Secure {
		t.Fatal("HomeNet should be marked secure (RSN)")
	}
	for _, n := range nets {
		if n.SSID == "OpenCafe" && n.Secure {
			t.Fatal("OpenCafe should be open")
		}
	}
}
