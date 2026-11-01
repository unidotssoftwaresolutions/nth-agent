package fakephone

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// errInvalidSelector is a selector this phone cannot read: Appium answers it "invalid selector", and so does this.
type errInvalidSelector struct{ message string }

func (e errInvalidSelector) Error() string { return e.message }

// find is every node the strategy and selector match, in document order, as UiAutomator2 returns them.
func find(root *node, using, value string) ([]*node, error) {
	var all []*node
	root.walk(func(n *node, _, _, _, _ int) { all = append(all, n) })
	var out []*node
	switch using {
	case "accessibility id":
		for _, n := range all {
			if n.desc == value {
				out = append(out, n)
			}
		}
	case "id":
		for _, n := range all {
			if n.rid != "" && (n.rid == value || n.rid == Package+":id/"+value || n.rid == "android:id/"+value) {
				out = append(out, n)
			}
		}
	case "class name":
		for _, n := range all {
			if n.class == value {
				out = append(out, n)
			}
		}
	case "-android uiautomator":
		selector, err := parseUiSelector(value)
		if err != nil {
			return nil, err
		}
		return selector.match(all), nil
	case "xpath":
		return evalXPath(root, value)
	default:
		return nil, errInvalidSelector{fmt.Sprintf("Locator Strategy '%s' is not supported for this session", using)}
	}
	return out, nil
}

// ── UiSelector ─────────────────────────────────────────────────────────────────────────────────────────────────────

type uiSelector struct {
	tests    []func(*node) bool
	instance int // -1 = every match
	scroll   bool
}

var uiCall = regexp.MustCompile(`^\.([A-Za-z]+)\(\s*("(?:[^"\\]|\\.)*"|-?\d+|true|false)\s*\)`)

// parseUiSelector reads `new UiSelector().text("…").instance(0)` and the scroll form
// `new UiScrollable(new UiSelector()…).scrollIntoView(new UiSelector()…)`, whose target is what it finds.
func parseUiSelector(expression string) (*uiSelector, error) {
	s := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(expression), ";"))
	scroll := false
	if strings.HasPrefix(s, "new UiScrollable(") {
		at := strings.LastIndex(s, ".scrollIntoView(")
		if at < 0 || !strings.HasSuffix(s, ")") {
			return nil, invalidUi(expression)
		}
		s = strings.TrimSpace(s[at+len(".scrollIntoView(") : len(s)-1])
		scroll = true
	}
	if !strings.HasPrefix(s, "new UiSelector()") {
		return nil, invalidUi(expression)
	}
	s = s[len("new UiSelector()"):]
	sel := &uiSelector{instance: -1, scroll: scroll}
	for s != "" {
		m := uiCall.FindStringSubmatch(s)
		if m == nil {
			return nil, invalidUi(expression)
		}
		s = s[len(m[0]):]
		method, raw := m[1], m[2]
		arg := raw
		if strings.HasPrefix(raw, `"`) {
			unquoted, err := strconv.Unquote(raw)
			if err != nil {
				return nil, invalidUi(expression)
			}
			arg = unquoted
		}
		test, err := uiTest(method, arg, sel)
		if err != nil {
			return nil, invalidUi(expression)
		}
		if test != nil {
			sel.tests = append(sel.tests, test)
		}
	}
	return sel, nil
}

func invalidUi(expression string) error {
	return errInvalidSelector{fmt.Sprintf("Could not parse UiSelector argument: %s", expression)}
}

func uiTest(method, arg string, sel *uiSelector) (func(*node) bool, error) {
	matches := func(get func(*node) string) (func(*node) bool, error) {
		rx, err := regexp.Compile("^(?:" + arg + ")$")
		if err != nil {
			return nil, err
		}
		return func(n *node) bool { return rx.MatchString(get(n)) }, nil
	}
	flag := func(get func(*node) bool) (func(*node) bool, error) {
		want, err := strconv.ParseBool(arg)
		if err != nil {
			return nil, err
		}
		return func(n *node) bool { return get(n) == want }, nil
	}
	text := func(n *node) string { return n.text }
	desc := func(n *node) string { return n.desc }
	switch method {
	case "text":
		return func(n *node) bool { return n.text == arg }, nil
	case "textContains":
		return func(n *node) bool { return strings.Contains(n.text, arg) }, nil
	case "textStartsWith":
		return func(n *node) bool { return strings.HasPrefix(n.text, arg) }, nil
	case "textMatches":
		return matches(text)
	case "description":
		return func(n *node) bool { return n.desc == arg }, nil
	case "descriptionContains":
		return func(n *node) bool { return strings.Contains(n.desc, arg) }, nil
	case "descriptionStartsWith":
		return func(n *node) bool { return strings.HasPrefix(n.desc, arg) }, nil
	case "descriptionMatches":
		return matches(desc)
	case "resourceId":
		return func(n *node) bool { return n.rid == arg }, nil
	case "resourceIdMatches":
		return matches(func(n *node) string { return n.rid })
	case "className":
		return func(n *node) bool { return n.class == arg }, nil
	case "classNameMatches":
		return matches(func(n *node) string { return n.class })
	case "packageName":
		return func(n *node) bool { return n.pkg == arg }, nil
	case "packageNameMatches":
		return matches(func(n *node) string { return n.pkg })
	case "clickable":
		return flag(func(n *node) bool { return n.clickable })
	case "focusable":
		return flag(func(n *node) bool { return n.focusable })
	case "focused":
		return flag(func(n *node) bool { return n.focused })
	case "scrollable":
		return flag(func(n *node) bool { return n.scrollable })
	case "enabled":
		return flag(func(*node) bool { return true })
	case "instance":
		i, err := strconv.Atoi(arg)
		if err != nil || i < 0 {
			return nil, fmt.Errorf("instance")
		}
		sel.instance = i
		return nil, nil
	}
	return nil, fmt.Errorf("unknown method %s", method)
}

func (s *uiSelector) match(all []*node) []*node {
	var out []*node
	for _, n := range all {
		ok := true
		for _, test := range s.tests {
			if !test(n) {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, n)
		}
	}
	if s.instance >= 0 {
		if s.instance < len(out) {
			return out[s.instance : s.instance+1]
		}
		return nil
	}
	return out
}

// ── XPath, the part of it selectors use ────────────────────────────────────────────────────────────────────────────

// evalXPath reads a location path of steps — `//` or `/`, then `*` or a class, then predicates: `[@attr='v']`,
// `[contains(@attr,'v')]`, `[starts-with(@attr,'v')]`, `[text()='v']`, `[n]`, joined with `and` / `or` — and answers
// "invalid selector" for anything else, as Appium does for an expression it cannot evaluate.
func evalXPath(root *node, expression string) ([]*node, error) {
	steps, err := parseXPath(expression)
	if err != nil {
		return nil, err
	}
	// The document's root is the hierarchy element; the app's tree hangs under it.
	hierarchy := &node{class: "hierarchy", children: []*node{root}}
	context := []*node{hierarchy}
	for _, step := range steps {
		var next []*node
		seen := map[*node]bool{}
		for _, c := range context {
			var candidates []*node
			if step.descendant {
				var rec func(n *node)
				rec = func(n *node) {
					for _, ch := range n.children {
						candidates = append(candidates, ch)
						rec(ch)
					}
				}
				rec(c)
			} else {
				candidates = append(candidates, c.children...)
			}
			var kept []*node
			for _, n := range candidates {
				if step.name != "*" && n.class != step.name {
					continue
				}
				ok := true
				for _, p := range step.filters {
					if !p(n) {
						ok = false
						break
					}
				}
				if ok {
					kept = append(kept, n)
				}
			}
			if step.position > 0 {
				if step.position <= len(kept) {
					kept = kept[step.position-1 : step.position]
				} else {
					kept = nil
				}
			}
			for _, n := range kept {
				if !seen[n] {
					seen[n] = true
					next = append(next, n)
				}
			}
		}
		context = next
	}
	return context, nil
}

type xpathStep struct {
	descendant bool
	name       string
	filters    []func(*node) bool
	position   int
}

var (
	xpathStepName = regexp.MustCompile(`^(\*|[A-Za-z_][A-Za-z0-9_.$]*)`)
	xpathCompare  = regexp.MustCompile(`^@([A-Za-z-]+)\s*=\s*('[^']*'|"[^"]*")`)
	xpathCall     = regexp.MustCompile(`^(contains|starts-with)\(\s*(@[A-Za-z-]+|text\(\))\s*,\s*('[^']*'|"[^"]*")\s*\)`)
	xpathText     = regexp.MustCompile(`^text\(\)\s*=\s*('[^']*'|"[^"]*")`)
	xpathHas      = regexp.MustCompile(`^@([A-Za-z-]+)`)
)

func parseXPath(expression string) ([]xpathStep, error) {
	invalid := errInvalidSelector{fmt.Sprintf("Invalid XPath expression: %s", expression)}
	s := strings.TrimSpace(expression)
	if !strings.HasPrefix(s, "/") {
		return nil, invalid
	}
	var steps []xpathStep
	for s != "" {
		var step xpathStep
		switch {
		case strings.HasPrefix(s, "//"):
			step.descendant, s = true, s[2:]
		case strings.HasPrefix(s, "/"):
			s = s[1:]
		default:
			return nil, invalid
		}
		m := xpathStepName.FindString(s)
		if m == "" {
			return nil, invalid
		}
		step.name, s = m, s[len(m):]
		for strings.HasPrefix(s, "[") {
			end := matchingBracket(s)
			if end < 0 {
				return nil, invalid
			}
			inner := strings.TrimSpace(s[1:end])
			s = s[end+1:]
			if n, err := strconv.Atoi(inner); err == nil {
				if n < 1 {
					return nil, invalid
				}
				step.position = n
				continue
			}
			test, ok := xpathPredicate(inner)
			if !ok {
				return nil, invalid
			}
			step.filters = append(step.filters, test)
		}
		steps = append(steps, step)
	}
	return steps, nil
}

func matchingBracket(s string) int {
	depth, quote := 0, byte(0)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"':
			quote = c
		case c == '[':
			depth++
		case c == ']':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// xpathPredicate reads `a and b`, `a or b` over the comparison forms (no parentheses around groups).
func xpathPredicate(expression string) (func(*node) bool, bool) {
	var terms []func(*node) bool
	var joins []string
	s := strings.TrimSpace(expression)
	for {
		term, rest, ok := xpathTerm(s)
		if !ok {
			return nil, false
		}
		terms = append(terms, term)
		rest = strings.TrimSpace(rest)
		if rest == "" {
			break
		}
		switch {
		case strings.HasPrefix(rest, "and "):
			joins, s = append(joins, "and"), rest[4:]
		case strings.HasPrefix(rest, "or "):
			joins, s = append(joins, "or"), rest[3:]
		default:
			return nil, false
		}
	}
	return func(n *node) bool {
		// `and` binds tighter than `or`, as in XPath.
		result, group := false, terms[0](n)
		for i, join := range joins {
			if join == "and" {
				group = group && terms[i+1](n)
			} else {
				result = result || group
				group = terms[i+1](n)
			}
		}
		return result || group
	}, true
}

func xpathTerm(s string) (func(*node) bool, string, bool) {
	s = strings.TrimSpace(s)
	unquote := func(q string) string { return q[1 : len(q)-1] }
	value := func(n *node, attr string) string {
		if attr == "text()" {
			return n.text
		}
		v, _ := (&node{}).attributeOf(n, strings.TrimPrefix(attr, "@"))
		return v
	}
	if m := xpathCall.FindStringSubmatch(s); m != nil {
		fn, attr, want := m[1], m[2], unquote(m[3])
		return func(n *node) bool {
			if fn == "contains" {
				return strings.Contains(value(n, attr), want)
			}
			return strings.HasPrefix(value(n, attr), want)
		}, s[len(m[0]):], true
	}
	if m := xpathCompare.FindStringSubmatch(s); m != nil {
		attr, want := m[1], unquote(m[2])
		return func(n *node) bool { return value(n, "@"+attr) == want }, s[len(m[0]):], true
	}
	if m := xpathText.FindStringSubmatch(s); m != nil {
		want := unquote(m[1])
		return func(n *node) bool { return n.text == want }, s[len(m[0]):], true
	}
	if m := xpathHas.FindStringSubmatch(s); m != nil {
		attr := m[1]
		return func(n *node) bool { return value(n, "@"+attr) != "" }, s[len(m[0]):], true
	}
	return nil, s, false
}

// attributeOf reads a node's own attribute for an XPath test (bounds are not compared by this subset).
func (*node) attributeOf(n *node, name string) (string, bool) {
	switch name {
	case "text":
		return n.text, true
	case "content-desc":
		return n.desc, true
	case "resource-id":
		return n.rid, true
	case "class":
		return n.class, true
	case "package":
		return n.pkg, true
	case "clickable":
		return strconv.FormatBool(n.clickable), true
	case "focusable":
		return strconv.FormatBool(n.focusable), true
	case "focused":
		return strconv.FormatBool(n.focused), true
	case "scrollable":
		return strconv.FormatBool(n.scrollable), true
	case "password":
		return strconv.FormatBool(n.password), true
	case "enabled", "displayed":
		return "true", true
	case "checkable", "checked", "selected", "long-clickable":
		return "false", true
	}
	return "", false
}
