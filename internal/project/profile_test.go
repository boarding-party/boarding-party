package project

import (
	"encoding/json"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

var playwrightDevices = map[string]bool{"iPhone 15": true, "iPhone 15 Pro Max": true, "Pixel 7": true,
	"Galaxy S24": true, "iPad (gen 11)": true, "iPad Mini": true}

func TestProfileOptionValues(t *testing.T) {
	re := regexp.MustCompile(`^[a-z0-9-]+$`)
	for _, f := range ProfileFields {
		seen := map[string]bool{}
		for _, o := range f.Options {
			if !re.MatchString(o.Value) || seen[o.Value] {
				t.Errorf("%s: bad or duplicate value %q", f.Key, o.Value)
			}
			seen[o.Value] = true
			if o.Label == "" || o.Guide == "" {
				t.Errorf("%s/%s: needs label and guide", f.Key, o.Value)
			}
		}
	}
}

func TestDeviceLaunch(t *testing.T) {
	var dev Field
	for _, f := range ProfileFields {
		if f.Key == "device" {
			dev = f
		}
	}
	if len(dev.Options) == 0 {
		t.Fatal("no device field")
	}
	for _, o := range dev.Options {
		pw, vp := DeviceLaunch(o.Value)
		if (pw == "") == (vp == "") {
			t.Errorf("%s: want exactly one of device/viewport, got %q %q", o.Value, pw, vp)
		}
		if pw != "" && !playwrightDevices[pw] {
			t.Errorf("%s: unknown Playwright device %q", o.Value, pw)
		}
	}
	if pw, vp := DeviceLaunch(""); pw != "" || vp != "1280x800" {
		t.Errorf("default: %q %q", pw, vp)
	}
	if pw, _ := DeviceLaunch("ipad"); pw != "iPad (gen 11)" {
		t.Errorf("ipad: %q", pw)
	}
}

func TestUnknownProfileValueRejected(t *testing.T) {
	_, err := LoadJSON([]byte(`{"name":"p","sites":["https://a.test"],"roles":[{"name":"r","personas":[{"name":"X","profile":{"device":"y"}}]}]}`))
	if err == nil || !strings.Contains(err.Error(), `persona "X": unknown device "y"`) {
		t.Fatalf("got %v", err)
	}
}

func TestTraitsOnlyRoundTrips(t *testing.T) {
	in := `{"name":"p","sites":["https://a.test"],"roles":[{"name":"r","personas":[{"name":"X","traits":["phone"]}]}]}`
	p, err := LoadJSON([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	if p.Roles[0].Personas[0].Profile != (Profile{}) {
		t.Fatal("profile not empty")
	}
	b, _ := json.Marshal(p)
	q, err := LoadJSON(b)
	if err != nil || !reflect.DeepEqual(p, q) {
		t.Fatalf("round trip: %v", err)
	}
}

func TestGuidanceOrder(t *testing.T) {
	if g := (Profile{}).Guidance(); len(g) != 0 {
		t.Fatalf("empty: %v", g)
	}
	p := Profile{Assistive: "screen-reader", Device: "iphone-15", Patience: "impatient"}
	g := p.Guidance()
	if len(g) != 3 || !strings.Contains(g[0], "iPhone 15") || !strings.Contains(g[1], "give up") || !strings.Contains(g[2], "accessibility snapshot") {
		t.Fatalf("guidance: %v", g)
	}
}

func TestLabels(t *testing.T) {
	got := Profile{Device: "ipad-mini", Age: "65-plus"}.Labels()
	want := map[string]string{"device": "iPad Mini", "age": "65 or over"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%v", got)
	}
}

func TestProfileFieldOrderMatchesStruct(t *testing.T) {
	typ := reflect.TypeOf(Profile{})
	if typ.NumField() != len(ProfileFields) {
		t.Fatalf("%d struct fields, %d catalogue fields", typ.NumField(), len(ProfileFields))
	}
	var p Profile
	v := reflect.ValueOf(&p).Elem()
	for i, f := range ProfileFields {
		key, _, _ := strings.Cut(typ.Field(i).Tag.Get("json"), ",")
		if key != f.Key {
			t.Errorf("field %d: struct key %q, catalogue key %q", i, key, f.Key)
		}
		v.Field(i).SetString(f.Options[0].Value)
	}
	for i, got := range p.values() {
		if got != ProfileFields[i].Options[0].Value {
			t.Errorf("values()[%d] = %q, want field %s", i, got, ProfileFields[i].Key)
		}
	}
}

func TestNoProfileOmitted(t *testing.T) {
	b, _ := json.Marshal(Persona{Name: "x"})
	if strings.Contains(string(b), "profile") {
		t.Fatalf("%s", b)
	}
}

func TestParseProfileTakesValuesOrLabels(t *testing.T) {
	p, err := ParseProfile(map[string]string{"device": "iPhone 15", "techLevel": "everyday", "patience": " gives up quickly "})
	if err != nil || p.Device != "iphone-15" || p.TechLevel != "everyday" || p.Patience != "impatient" {
		t.Fatalf("%+v %v", p, err)
	}
	if _, err := ParseProfile(map[string]string{"device": "toaster"}); err == nil || !strings.Contains(err.Error(), "iphone-15") {
		t.Fatalf("unknown value should list the options: %v", err)
	}
	if _, err := ParseProfile(map[string]string{"mood": "calm"}); err == nil || !strings.Contains(err.Error(), "techLevel") {
		t.Fatalf("unknown field should list the fields: %v", err)
	}
}
