package fakephone

import (
	"fmt"
	"strconv"
	"strings"
)

// node is one view on the phone's screen, with the attributes UiAutomator2 writes for it.
type node struct {
	class, pkg, text, desc, rid string
	// key names the view for the app's own listeners (a tap on it) and for an element id that must survive a new dump of the
	// same screen. Views nothing acts on have none.
	key string

	clickable, focusable, focused, longClickable, scrollable, password, showingHint bool
	l, t, r, b                                                                      int
	children                                                                        []*node
}

func view(class string, l, t, r, b int, children ...*node) *node {
	return &node{class: class, l: l, t: t, r: r, b: b, children: children}
}

func setPackage(n *node, pkg string) {
	n.pkg = pkg
	for _, c := range n.children {
		setPackage(c, pkg)
	}
}

// clipped is the node's bounds as UiAutomator2 reports them: cut to its parent's and to the screen.
func clip(l, t, r, b, pl, pt, pr, pb int) (int, int, int, int) {
	l, t, r, b = max(l, pl), max(t, pt), min(r, pr), min(b, pb)
	if r < l {
		r = l
	}
	if b < t {
		b = t
	}
	return l, t, r, b
}

// walk visits every node with its clipped bounds, parent first.
func (n *node) walk(visit func(n *node, l, t, r, b int)) {
	var rec func(n *node, pl, pt, pr, pb int)
	rec = func(n *node, pl, pt, pr, pb int) {
		l, t, r, b := clip(n.l, n.t, n.r, n.b, pl, pt, pr, pb)
		visit(n, l, t, r, b)
		for _, c := range n.children {
			rec(c, l, t, r, b)
		}
	}
	rec(n, 0, 0, Width, Height)
}

func (n *node) byKey(key string) *node {
	var found *node
	n.walk(func(m *node, _, _, _, _ int) {
		if found == nil && m.key == key {
			found = m
		}
	})
	return found
}

// bounds is the node's clipped bounds in this tree.
func (root *node) bounds(target *node) (int, int, int, int) {
	var l, t, r, b int
	root.walk(func(m *node, ml, mt, mr, mb int) {
		if m == target {
			l, t, r, b = ml, mt, mr, mb
		}
	})
	return l, t, r, b
}

// at is the deepest node that takes a tap at (x, y): the last clickable node whose bounds hold the point, or the deepest
// node of any kind when nothing there is clickable.
func (root *node) at(x, y int) *node {
	var hit, any *node
	root.walk(func(m *node, l, t, r, b int) {
		if x >= l && x < r && y >= t && y < b {
			any = m
			if m.clickable {
				hit = m
			}
		}
	})
	if hit != nil {
		return hit
	}
	return any
}

// clickTarget is the key a tap on this node acts on: its own, or the closest clickable ancestor's (a tap on a card's
// name is a tap on the card).
func (root *node) clickTarget(target *node) string {
	var path []*node
	var rec func(n *node) bool
	rec = func(n *node) bool {
		path = append(path, n)
		if n == target {
			return true
		}
		for _, c := range n.children {
			if rec(c) {
				return true
			}
		}
		path = path[:len(path)-1]
		return false
	}
	if !rec(root) {
		return ""
	}
	for i := len(path) - 1; i >= 0; i-- {
		if path[i].clickable && path[i].key != "" {
			return path[i].key
		}
	}
	return target.key
}

// attribute is one of the node's attributes by its UiAutomator2 name, as the attribute command answers it.
func (root *node) attribute(n *node, name string) (string, bool) {
	l, t, r, b := root.bounds(n)
	switch name {
	case "text", "name":
		return n.text, true
	case "content-desc", "contentDescription":
		return n.desc, true
	case "resource-id", "resourceId":
		return n.rid, true
	case "class", "className":
		return n.class, true
	case "package":
		return n.pkg, true
	case "bounds":
		return fmt.Sprintf("[%d,%d][%d,%d]", l, t, r, b), true
	case "displayed":
		return strconv.FormatBool(r > l && b > t), true
	case "enabled":
		return "true", true
	case "clickable":
		return strconv.FormatBool(n.clickable), true
	case "focusable":
		return strconv.FormatBool(n.focusable), true
	case "focused":
		return strconv.FormatBool(n.focused), true
	case "long-clickable", "longClickable":
		return strconv.FormatBool(n.longClickable), true
	case "scrollable":
		return strconv.FormatBool(n.scrollable), true
	case "password":
		return strconv.FormatBool(n.password), true
	case "checkable", "checked", "selected":
		return "false", true
	case "showing-hint", "hint":
		return strconv.FormatBool(n.showingHint), true
	}
	return "", false
}

// pageSource writes the tree the way UiAutomator2's dump does: the hierarchy element, then every view with the
// attributes a real dump carries, in the same order.
func (root *node) pageSource() string {
	var out strings.Builder
	out.WriteString("<?xml version='1.0' encoding='UTF-8' standalone='yes' ?>\n")
	fmt.Fprintf(&out, `<hierarchy index="0" class="hierarchy" rotation="0" width="%d" height="%d">`+"\n", Width, Height)
	order := 0
	var rec func(n *node, index, depth, pl, pt, pr, pb int)
	rec = func(n *node, index, depth, pl, pt, pr, pb int) {
		l, t, r, b := clip(n.l, n.t, n.r, n.b, pl, pt, pr, pb)
		indent := strings.Repeat("  ", depth)
		fmt.Fprintf(&out, `%s<%s index="%d" package="%s" class="%s" text="%s"`, indent, n.class, index, n.pkg, n.class, escape(n.text))
		if n.desc != "" {
			fmt.Fprintf(&out, ` content-desc="%s"`, escape(n.desc))
		}
		if n.rid != "" {
			fmt.Fprintf(&out, ` resource-id="%s"`, n.rid)
		}
		fmt.Fprintf(&out, ` checkable="false" checked="false" clickable="%t" enabled="true" focusable="%t" focused="%t"`+
			` long-clickable="%t" password="%t" scrollable="%t" selected="false" bounds="[%d,%d][%d,%d]" displayed="true"`+
			` a11y-important="%t" screen-reader-focusable="false" drawing-order="%d" showing-hint="%t" text-entry-key="false"`+
			` dismissable="false" a11y-focused="false" heading="false" live-region="0" context-clickable="false"`+
			` content-invalid="false" window-id="613"`,
			n.clickable, n.focusable, n.focused, n.longClickable, n.password, n.scrollable, l, t, r, b,
			n.clickable || n.text != "" || n.desc != "", order%7, n.showingHint)
		order++
		if len(n.children) == 0 {
			out.WriteString(" />\n")
			return
		}
		out.WriteString(">\n")
		for i, c := range n.children {
			rec(c, i, depth+1, l, t, r, b)
		}
		fmt.Fprintf(&out, "%s</%s>\n", indent, n.class)
	}
	rec(root, 0, 1, 0, 0, Width, Height)
	out.WriteString("</hierarchy>")
	return out.String()
}

func escape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;", "\n", "&#10;").Replace(s)
}
