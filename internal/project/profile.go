package project

import (
	"fmt"
	"slices"
	"strings"
)

// Profile is a persona's structured background. Every field is optional; a
// value is a stable key from ProfileFields (labels are display-only).
type Profile struct {
	Device    string `json:"device,omitempty"`
	Age       string `json:"age,omitempty"`
	TechLevel string `json:"techLevel,omitempty"`
	Patience  string `json:"patience,omitempty"`
	Reading   string `json:"reading,omitempty"`
	Assistive string `json:"assistive,omitempty"`
	Vision    string `json:"vision,omitempty"`
	Display   string `json:"display,omitempty"`
}

// Option is one choice of a profile field. Guide is the sentence the agent
// reads; it is never sent to the UI.
type Option struct{ Value, Label, Guide string }

// Field is one profile question. The API sends it through its own wire types.
type Field struct {
	Key, Label, Hint string
	Options          []Option
}

// ProfileFields is the catalogue, in form order.
var ProfileFields = []Field{
	{Key: "device", Label: "Device", Hint: "Sets the browser's screen size and touch behaviour. Not set means a 1280×800 desktop.", Options: []Option{
		{"desktop", "Desktop 1280×800", "You are on a desktop computer with a 1280 by 800 screen, using a mouse and keyboard."},
		{"desktop-large", "Large desktop 1920×1080", "You are on a large desktop monitor with a 1920 by 1080 screen, using a mouse and keyboard."},
		{"laptop-small", "Small laptop 1366×768", "You are on a small laptop with a 1366 by 768 screen, so less of the page is visible at once."},
		{"iphone-15", "iPhone 15", "You are on an iPhone 15, using touch only, with a small screen."},
		{"iphone-15-pro-max", "iPhone 15 Pro Max", "You are on an iPhone 15 Pro Max, using touch only, with a large phone screen."},
		{"pixel-7", "Android phone (Pixel 7)", "You are on an Android phone (a Pixel 7), using touch only, with a small screen."},
		{"galaxy-s24", "Samsung Galaxy S24", "You are on a Samsung Galaxy S24 phone, using touch only, with a small screen."},
		{"ipad", "iPad", "You are on an iPad, using touch only, with a tablet-sized screen."},
		{"ipad-mini", "iPad Mini", "You are on an iPad Mini, using touch only, with a small tablet screen."},
	}},
	{Key: "age", Label: "Age", Hint: "Shapes vocabulary, expectations and how forgiving the persona is.", Options: []Option{
		{"18-24", "18-24", "You are aged 18 to 24 and expect things to be quick and familiar from other apps."},
		{"25-34", "25-34", "You are aged 25 to 34 and are comfortable with most online services."},
		{"35-49", "35-49", "You are aged 35 to 49, busy, and want to get things done without fuss."},
		{"50-64", "50-64", "You are aged 50 to 64 and prefer clear labels and familiar patterns to clever design."},
		{"65-plus", "65 or over", "You are aged 65 or over and read carefully and mistrust anything unclear: hesitate before steps that look risky and report anything unclear, but still complete the story."},
	}},
	{Key: "techLevel", Label: "Tech confidence", Hint: "How well the persona copes with unfamiliar interfaces.", Options: []Option{
		{"new", "New to the internet", "You are new to the internet: you do not know common conventions, and you get lost when labels use jargon."},
		{"everyday", "Everyday user", "You use the web daily for shopping and banking, but you do not know technical terms."},
		{"confident", "Confident", "You are confident online and expect standard conventions such as search, filters and breadcrumbs to work."},
		{"expert", "Expert", "You are a technical expert who notices broken conventions, slow pages and sloppy details."},
	}},
	{Key: "patience", Label: "Patience", Hint: "When the persona gives up.", Options: []Option{
		{"patient", "Patient", "You are patient: you will retry and explore several routes before you decide you are stuck."},
		{"normal", "Normal", "You have normal patience: you will try a couple of routes, then give up if nothing works."},
		{"impatient", "Gives up quickly", "If something takes more than a few seconds or two tries, give up on the story and end the run as stuck, saying why, after the usual wrap-up steps (stop recording, upload, save)."},
	}},
	{Key: "reading", Label: "Reading", Hint: "How the persona deals with text on the page.", Options: []Option{
		{"careful", "Reads everything", "You read every word on a page before you act, including the small print."},
		{"skims", "Skims", "You skim: you act on headings and buttons and ignore long paragraphs, so report anything important hidden in body text."},
		{"struggles", "Finds long text hard", "You find long text hard, so report any wording or layout that is dense or confusing, and avoid reading long passages."},
		{"second-language", "English is a second language", "English is your second language: idioms and complex sentences confuse you, so report any wording you would not understand."},
	}},
	{Key: "assistive", Label: "Assistive technology", Hint: "The browser enforces it: a screen reader user sees no screenshots, a keyboard user cannot click, and zoom renders the page at 200%.", Options: []Option{
		{"none", "None", "You use no assistive technology."},
		{"screen-reader", "Screen reader", "You use a screen reader: you never see the page, only its accessibility snapshot, so judge the page by what the snapshot tells you and report anything a screen reader user would miss or be misled by."},
		{"keyboard-only", "Keyboard only", "You use only the keyboard and the browser gives you no clicking: move with browser_press_key (Tab, Shift+Tab, arrow keys, Enter, Space) and type into the focused field with browser_type_focused. Report anything you cannot reach or operate, and anywhere you cannot see where focus is."},
		{"zoom", "Large text / zoom", "Your browser is zoomed to 200 per cent, so the page you see is laid out as it really is at that size; report anything cut off, overlapping, hidden or too small to read."},
	}},
	{Key: "vision", Label: "Eyesight", Hint: "The browser draws the page as the persona sees it, in screenshots and on the live screen.", Options: []Option{
		{"blurred", "Blurred vision", "Your eyesight is blurred and the page you see is blurred as it would be for you: report text you cannot read and controls you cannot make out, judging by the screenshots, not the snapshot."},
		{"low-contrast", "Low contrast sensitivity", "You struggle to tell apart colours of similar brightness and the page you see shows it: report text, borders and controls that fade into their background."},
		{"deuteranopia", "Red-green colour blindness (deuteranopia)", "You have red-green colour blindness and the page you see shows the colours as you see them: report anything that relies on red or green alone, such as errors, statuses or required fields."},
		{"protanopia", "Red colour blindness (protanopia)", "You have protanopia, so reds look dark and dull, and the page you see shows the colours as you see them: report anything that relies on colour alone."},
		{"tritanopia", "Blue-yellow colour blindness (tritanopia)", "You have blue-yellow colour blindness and the page you see shows the colours as you see them: report anything that relies on blue or yellow alone."},
		{"achromatopsia", "No colour vision (achromatopsia)", "You see no colour at all and the page you see is in greys: report anything you can only tell apart by its colour."},
	}},
	{Key: "display", Label: "Display settings", Hint: "System settings the browser applies, which the site can read.", Options: []Option{
		{"reduced-motion", "Reduce motion", "Your device asks sites to reduce motion: report animations that still move, and anything you must wait for an animation to reach."},
		{"high-contrast", "High contrast", "Your device uses a high-contrast theme that replaces the site's colours: report anything that disappears, loses its outline or cannot be told apart."},
		{"dark", "Dark mode", "Your device is set to dark mode: report text, images or controls that are hard to see in it."},
	}},
}

func (p Profile) values() []string {
	return []string{p.Device, p.Age, p.TechLevel, p.Patience, p.Reading, p.Assistive, p.Vision, p.Display}
}

func (f Field) option(value string) (Option, bool) {
	for _, o := range f.Options {
		if o.Value == value {
			return o, true
		}
	}
	return Option{}, false
}

// Guidance returns one Guide sentence per set field, in ProfileFields order.
func (p Profile) Guidance() []string {
	out := []string{}
	for i, v := range p.values() {
		if o, ok := ProfileFields[i].option(v); ok {
			out = append(out, o.Guide)
		}
	}
	return out
}

// Labels maps each set field's key to its option's display label.
func (p Profile) Labels() map[string]string {
	out := map[string]string{}
	for i, v := range p.values() {
		if o, ok := ProfileFields[i].option(v); ok {
			out[ProfileFields[i].Key] = o.Label
		}
	}
	return out
}

// ParseProfile reads a profile written by an agent: keys as in JSON, each
// value either an option's value or its label, ignoring case and spaces.
// Errors list what is allowed, so the agent can try again.
func ParseProfile(m map[string]string) (Profile, error) {
	var p Profile
	slots := map[string]*string{"device": &p.Device, "age": &p.Age, "techLevel": &p.TechLevel,
		"patience": &p.Patience, "reading": &p.Reading, "assistive": &p.Assistive, "vision": &p.Vision, "display": &p.Display}
	for k, v := range m {
		slot, ok := slots[k]
		if !ok {
			keys := make([]string, len(ProfileFields))
			for i, f := range ProfileFields {
				keys[i] = f.Key
			}
			return Profile{}, fmt.Errorf("unknown field %q; fields are %s", k, strings.Join(keys, ", "))
		}
		f := ProfileFields[slices.IndexFunc(ProfileFields, func(f Field) bool { return f.Key == k })]
		want := strings.TrimSpace(v)
		if want == "" {
			continue
		}
		var vals []string
		for _, o := range f.Options {
			if strings.EqualFold(o.Value, want) || strings.EqualFold(o.Label, want) {
				*slot = o.Value
				break
			}
			vals = append(vals, o.Value)
		}
		if *slot == "" {
			return Profile{}, fmt.Errorf("unknown %s %q; use one of %s", k, v, strings.Join(vals, ", "))
		}
	}
	return p, nil
}

// validate checks every set value against its field's options.
func (p Profile) validate() error {
	for i, v := range p.values() {
		if v == "" {
			continue
		}
		if _, ok := ProfileFields[i].option(v); !ok {
			return &unknownValueError{ProfileFields[i].Key, v}
		}
	}
	return nil
}

type unknownValueError struct{ key, value string }

func (e *unknownValueError) Error() string { return "unknown " + e.key + " " + `"` + e.value + `"` }

// BrowserSettings are the set fields the worker turns into browser settings
// (worker/bp-a11y.mjs), by profile key; "none" is left out.
func (p Profile) BrowserSettings() map[string]string {
	out := map[string]string{}
	for k, v := range map[string]string{"device": p.Device, "assistive": p.Assistive, "vision": p.Vision, "display": p.Display} {
		if v != "" && v != "none" {
			out[k] = v
		}
	}
	return out
}

// Validate checks every set value against its field's options.
func (p Profile) Validate() error { return p.validate() }
