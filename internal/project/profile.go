package project

// Profile is a persona's structured background. Every field is optional; a
// value is a stable key from ProfileFields (labels are display-only).
type Profile struct {
	Device    string `json:"device,omitempty"`
	Age       string `json:"age,omitempty"`
	TechLevel string `json:"techLevel,omitempty"`
	Patience  string `json:"patience,omitempty"`
	Reading   string `json:"reading,omitempty"`
	Assistive string `json:"assistive,omitempty"`
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
	{Key: "assistive", Label: "Assistive technology", Hint: "Accessibility needs the persona browses with.", Options: []Option{
		{"none", "None", "You use no assistive technology."},
		{"screen-reader", "Screen reader", "Navigate only by the accessibility snapshot; never rely on how the page looks, and report anything a screen reader would miss."},
		{"keyboard-only", "Keyboard only", "Use only the keyboard (Tab, Shift+Tab, Enter, Space, arrow keys); never click, and report anything you cannot reach or see focus on."},
		{"zoom", "Large text / zoom", "Browse as someone who has enlarged text to 200 per cent; report anything that would be cut off, overlap or be too small to read at that size."},
	}},
}

func (p Profile) values() []string {
	return []string{p.Device, p.Age, p.TechLevel, p.Patience, p.Reading, p.Assistive}
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

const defaultViewport = "1280x800"

var viewports = map[string]string{
	"desktop":       "1280x800",
	"desktop-large": "1920x1080",
	"laptop-small":  "1366x768",
}

var playwrightDeviceNames = map[string]string{
	"iphone-15":         "iPhone 15",
	"iphone-15-pro-max": "iPhone 15 Pro Max",
	"pixel-7":           "Pixel 7",
	"galaxy-s24":        "Galaxy S24",
	"ipad":              "iPad (gen 11)",
	"ipad-mini":         "iPad Mini",
}

// DeviceLaunch maps a device value to browser launch settings: a Playwright
// device name for phones and tablets, or a WIDTHxHEIGHT viewport for desktops.
// Exactly one result is non-empty; unset or unknown values get the default
// desktop viewport.
func DeviceLaunch(device string) (playwrightDevice, viewport string) {
	if d, ok := playwrightDeviceNames[device]; ok {
		return d, ""
	}
	if v, ok := viewports[device]; ok {
		return "", v
	}
	return "", defaultViewport
}

// Validate checks every set value against its field's options.
func (p Profile) Validate() error { return p.validate() }
