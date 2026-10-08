package privacy

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// textUnit maps a decoded string's byte boundaries back to the original input.
// Replacements are encoded through the same layers (JSON, URL, quoted fields).
// Untouched bytes, object order, duplicate keys and exact numbers stay intact.
type textUnit struct {
	text      string
	base      int
	positions []int
	encode    func(string) string
	markers   [][]int
}

// Markers may already have crossed a URL boundary. Protect their encoded
// spelling too when a custom text rule scans the surrounding diagnostic text.
var encodedMarkerRE = regexp.MustCompile(`%(?:25)*5[Bb]redacted(?::|%(?:25)*3[Aa])[0-9a-f]{24}%(?:25)*5[Dd]`)

func protectedMarkers(text string) [][]int {
	markers := markerRE.FindAllStringIndex(text, -1)
	if !strings.Contains(text, "%") {
		return markers
	}
	encoded := encodedMarkerRE.FindAllStringIndex(text, -1)
	if len(markers) == 0 {
		return encoded
	}
	if len(encoded) == 0 {
		return markers
	}
	markers = append(markers, encoded...)
	sort.Slice(markers, func(i, j int) bool { return markers[i][0] < markers[j][0] })
	return markers
}

func rawUnit(text string) textUnit {
	return textUnit{text: text, encode: func(value string) string { return value }, markers: protectedMarkers(text)}
}

func (u textUnit) position(index int) int {
	if u.positions != nil {
		return u.positions[index]
	}
	return u.base + index
}

func (u textUnit) sub(start, end int) textUnit {
	child := textUnit{text: u.text[start:end], base: u.position(start), encode: u.encode}
	if u.positions != nil {
		child.positions = u.positions[start : end+1]
	}
	for _, marker := range u.markers {
		if marker[0] < end && marker[1] > start {
			child.markers = append(child.markers, []int{max(marker[0], start) - start, min(marker[1], end) - start})
		}
	}
	return child
}

func (u textUnit) allows(start, end int) bool {
	if start >= end {
		return false
	}
	for _, marker := range u.markers {
		if start < marker[1] && end > marker[0] {
			return false
		}
	}
	return true
}

func jsonEscape(value string) string {
	raw, _ := json.Marshal(value)
	return string(raw[1 : len(raw)-1])
}

func (u textUnit) decodeJSON(start, end int) textUnit {
	raw := u.text[start:end]
	var decoded string
	_ = json.Unmarshal([]byte(raw), &decoded)
	positions := make([]int, len(decoded)+1)
	for source, dest := 1, 0; source < len(raw)-1; {
		_, size := utf8.DecodeRuneInString(decoded[dest:])
		_, width := utf8.DecodeRuneInString(raw[source:])
		if raw[source] == '\\' {
			width = 2
			if raw[source+1] == 'u' {
				width = 6
				if size == 4 && source+12 <= len(raw)-1 && raw[source+6:source+8] == `\u` {
					width = 12
				}
			}
		}
		for offset := 0; offset < size; offset++ {
			positions[dest+offset] = u.position(start + source)
		}
		source, dest = source+width, dest+size
		positions[dest] = u.position(start + source)
	}
	if decoded == "" {
		positions[0] = u.position(start + 1)
	}
	return textUnit{text: decoded, positions: positions, encode: func(value string) string { return u.encode(jsonEscape(value)) }, markers: protectedMarkers(decoded)}
}

func (u textUnit) decodeURL(start, end int, query bool) (textUnit, bool) {
	raw := u.text[start:end]
	decode, encode := url.PathUnescape, url.PathEscape
	if query {
		decode, encode = url.QueryUnescape, url.QueryEscape
	}
	decoded, err := decode(raw)
	if err != nil {
		return textUnit{}, false
	}
	positions := make([]int, len(decoded)+1)
	source := 0
	for dest := range len(decoded) {
		positions[dest] = u.position(start + source)
		if raw[source] == '%' {
			source += 3
		} else {
			source++
		}
	}
	positions[len(decoded)] = u.position(end)
	return textUnit{text: decoded, positions: positions, encode: func(value string) string { return u.encode(encode(value)) }, markers: protectedMarkers(decoded)}, true
}

func (u textUnit) decodeSingle(start, end int) textUnit {
	raw := u.text[start+1 : end-1]
	var decoded strings.Builder
	positions := make([]int, 0, len(raw)+1)
	for source := 0; source < len(raw); {
		char, _, tail, err := strconv.UnquoteChar(raw[source:], '\'')
		width := len(raw[source:]) - len(tail)
		if err != nil {
			char, width = utf8.DecodeRuneInString(raw[source:])
		}
		decoded.WriteRune(char)
		for range utf8.RuneLen(char) {
			positions = append(positions, u.position(start+1+source))
		}
		source += width
	}
	positions = append(positions, u.position(end-1))
	value := decoded.String()
	return textUnit{text: value, positions: positions, encode: u.encode, markers: protectedMarkers(value)}
}

type replacement struct {
	start, end       int
	hitStart, hitEnd int
	value            string
	id               string
	order            int
}

type Hit struct {
	RuleID string `json:"rule_id"`
	Count  int    `json:"count"`
}

type redactionPlan struct {
	r         *Redactor
	input     string
	inputUnit textUnit
	units     []textUnit
	spans     []replacement
}

func newRedactionPlan(r *Redactor, input string) *redactionPlan {
	return &redactionPlan{r: r, input: input, inputUnit: rawUnit(input)}
}

func (p *redactionPlan) root() textUnit { return p.inputUnit }

func (p *redactionPlan) add(u textUnit, start, end int, value, id string) {
	if !u.allows(start, end) {
		return
	}
	left, right := u.position(start), u.position(end)
	encoded := u.encode(value)
	if left < right && p.input[left:right] != encoded {
		p.spans = append(p.spans, replacement{start: left, end: right, hitStart: left, hitEnd: right, value: encoded, id: id, order: p.r.policy.order[id]})
	}
}

func (p *redactionPlan) mask(u textUnit, id string) {
	start := 0
	maskPart := func(end int) {
		if start < end {
			value := u.text[start:end]
			p.r.Register(id, value)
			before := len(p.spans)
			p.add(u, start, end, Mask(value), id)
			if len(p.spans) > before {
				p.spans[before].hitStart, p.spans[before].hitEnd = u.position(0), u.position(len(u.text))
			}
		}
	}
	for _, marker := range u.markers {
		maskPart(marker[0])
		start = marker[1]
	}
	maskPart(len(u.text))
}

func (p *redactionPlan) custom(u textUnit) {
	for _, rule := range p.r.policy.custom {
		var matches [][]int
		switch rule.Kind {
		case Literal:
			for start := 0; start < len(u.text); {
				offset := strings.Index(u.text[start:], rule.Pattern)
				if offset < 0 {
					break
				}
				start += offset
				matches = append(matches, []int{start, start + len(rule.Pattern)})
				start++
			}
		case Regex:
			matches = rule.regex.FindAllStringIndex(u.text, -1)
		}
		for _, match := range matches {
			p.mask(u.sub(match[0], match[1]), rule.ID)
		}
	}
}

var assignmentRE = regexp.MustCompile(`(?i)(["']?([^\s"'=:,;{}\[\]&?]+)["']?\s*[:=]\s*)("(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|[^\s,;&}\]\r\n]+)`)

func (p *redactionPlan) plain(u textUnit) {
	if u.text == "" {
		return
	}
	p.units = append(p.units, u)
	if p.r.RuleEnabled(AuthText) && strings.ContainsAny(u.text, "Bb") {
		for _, match := range authRE.FindAllStringSubmatchIndex(u.text, -1) {
			if u.allows(match[4], match[5]) {
				p.mask(u.sub(match[4], match[5]), AuthText)
			}
		}
	}
	if !strings.ContainsAny(u.text, ":=") {
		return
	}
	for _, match := range assignmentRE.FindAllStringSubmatchIndex(u.text, -1) {
		name := u.text[match[4]:match[5]]
		sources := p.r.fieldSources(name, TextFields)
		if len(sources) == 0 {
			continue
		}
		content := u.text[match[6]:match[7]]
		if strings.Contains(strings.ToLower(name), "authorization") && (strings.EqualFold(content, "Bearer") || strings.EqualFold(content, "Basic")) {
			continue
		}
		value := u.sub(match[6], match[7])
		if strings.HasPrefix(content, `"`) && json.Valid([]byte(content)) {
			value = u.decodeJSON(match[6], match[7])
		} else if strings.HasPrefix(content, "'") && strings.HasSuffix(content, "'") {
			value = u.decodeSingle(match[6], match[7])
		}
		for _, id := range sources {
			p.mask(value, id)
		}
	}
}

func (p *redactionPlan) text(u textUnit) {
	// Literal/regex rules also see the entire diagnostic string, so matches
	// may cross URL boundaries. Parsed URL values additionally expose encodings.
	p.custom(u)
	if !strings.Contains(u.text, "://") {
		p.plain(u)
		return
	}
	start := 0
	for _, match := range urlRE.FindAllStringIndex(u.text, -1) {
		p.plain(u.sub(start, match[0]))
		raw := u.text[match[0]:match[1]]
		trimmed := strings.TrimRight(raw, "),;]}")
		p.url(u.sub(match[0], match[0]+len(trimmed)))
		p.plain(u.sub(match[0]+len(trimmed), match[1]))
		start = match[1]
	}
	p.plain(u.sub(start, len(u.text)))
}

func (p *redactionPlan) url(u textUnit) {
	parsed, err := url.Parse(u.text)
	if err != nil {
		if p.r.RuleEnabled(URLUserinfo) {
			p.mask(u, URLUserinfo)
		}
		p.plain(u)
		return
	}
	// Exclude delimiters and field names from value matching, while retaining
	// the original URL spelling/order instead of reserializing url.Values.
	authorityStart, authorityEnd := 0, 0
	if separator := strings.Index(u.text, "://"); separator >= 0 {
		authorityStart = separator + 3
	} else if strings.HasPrefix(u.text, "//") {
		authorityStart = 2
	}
	if authorityStart > 0 {
		authorityEnd = len(u.text)
		if end := strings.IndexAny(u.text[authorityStart:], "/?#"); end >= 0 {
			authorityEnd = authorityStart + end
		}
	}
	addValue := func(start, end int, query, userinfo bool, ids []string) {
		if value, ok := u.decodeURL(start, end, query); ok {
			if userinfo {
				// PathEscape leaves colons intact, which would turn a username's
				// marker into a user/password separator on the next pass.
				value.encode = func(text string) string { return u.encode(url.User(text).String()) }
			}
			p.units = append(p.units, value)
			p.custom(value)
			for _, id := range ids {
				p.mask(value, id)
			}
		} else {
			value := u.sub(start, end)
			p.units = append(p.units, value)
			p.custom(value)
		}
	}
	if parsed.User != nil && authorityEnd > authorityStart {
		at := strings.LastIndex(u.text[authorityStart:authorityEnd], "@") + authorityStart
		var ids []string
		if p.r.RuleEnabled(URLUserinfo) {
			ids = []string{URLUserinfo}
			password, _ := parsed.User.Password()
			p.r.Register(URLUserinfo, base64.StdEncoding.EncodeToString([]byte(parsed.User.Username()+":"+password)))
		}
		if colon := strings.IndexByte(u.text[authorityStart:at], ':'); colon >= 0 {
			addValue(authorityStart, authorityStart+colon, false, true, ids)
			addValue(authorityStart+colon+1, at, false, true, ids)
		} else {
			addValue(authorityStart, at, false, true, ids)
		}
		addValue(at+1, authorityEnd, false, false, nil)
	} else if authorityEnd > authorityStart {
		addValue(authorityStart, authorityEnd, false, false, nil)
	}
	fragment := strings.IndexByte(u.text, '#')
	end := len(u.text)
	if fragment >= 0 {
		end = fragment
		addValue(fragment+1, len(u.text), false, false, nil)
	}
	query := strings.IndexByte(u.text[:end], '?')
	pathEnd := end
	if query >= 0 {
		pathEnd = query
		if _, err := url.ParseQuery(parsed.RawQuery); err != nil && p.r.RuleEnabled(URLQuery) {
			value := u.sub(query+1, end)
			p.r.Register(URLQuery, value.text)
			p.add(value, 0, len(value.text), "redacted="+url.QueryEscape(Mask(value.text)), URLQuery)
		} else {
			for start := query + 1; start <= end; {
				partEnd := end
				if offset := strings.IndexByte(u.text[start:end], '&'); offset >= 0 {
					partEnd = start + offset
				}
				if equal := strings.IndexByte(u.text[start:partEnd], '='); equal >= 0 {
					name, err := url.QueryUnescape(u.text[start : start+equal])
					if err == nil {
						addValue(start+equal+1, partEnd, true, false, p.r.fieldSources(name, URLQuery))
					}
				}
				start = partEnd + 1
			}
		}
	}
	if authorityEnd <= pathEnd {
		addValue(authorityEnd, pathEnd, false, false, nil)
	}
}

func boundary(text string, index int) bool {
	if index < 0 || index >= len(text) {
		return true
	}
	char := text[index]
	return !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '_')
}

func (p *redactionPlan) known() {
	matcher := p.r.policyMatcherSnapshot()
	for _, u := range p.units {
		for _, entry := range matcher.entries {
			for offset := 0; offset < len(u.text); {
				start := strings.Index(u.text[offset:], entry.value)
				if start < 0 {
					break
				}
				start += offset
				end := start + len(entry.value)
				if u.allows(start, end) {
					for _, source := range entry.sources {
						if source.substring || len(entry.value) >= 4 || boundary(u.text, start-1) && boundary(u.text, end) {
							p.add(u, start, end, source.masked, source.id)
						}
					}
				}
				offset = start + 1
			}
		}
	}
}

func (p *redactionPlan) finish(collect bool) (string, []Hit) {
	p.known()
	if len(p.spans) == 0 {
		return p.input, []Hit{}
	}
	sort.Slice(p.spans, func(i, j int) bool {
		a, b := p.spans[i], p.spans[j]
		if a.start != b.start {
			return a.start < b.start
		}
		if a.end != b.end {
			return a.end > b.end
		}
		if a.order != b.order {
			return a.order < b.order
		}
		return a.value < b.value
	})
	var hits []Hit
	if collect {
		detections := append([]replacement(nil), p.spans...)
		sort.Slice(detections, func(i, j int) bool {
			if detections[i].hitStart != detections[j].hitStart {
				return detections[i].hitStart < detections[j].hitStart
			}
			return detections[i].hitEnd > detections[j].hitEnd
		})
		lastEnd, counts := map[string]int{}, map[string]int{}
		for _, span := range detections {
			end, exists := lastEnd[span.id]
			if !exists || span.hitStart >= end {
				counts[span.id]++
			}
			if !exists || span.hitEnd > end {
				lastEnd[span.id] = span.hitEnd
			}
		}
		for id, count := range counts {
			hits = append(hits, Hit{id, count})
		}
		sort.Slice(hits, func(i, j int) bool { return p.r.policy.order[hits[i].RuleID] < p.r.policy.order[hits[j].RuleID] })
	}
	var output strings.Builder
	start := 0
	for _, span := range p.spans {
		if span.start < start {
			continue
		}
		output.WriteString(p.input[start:span.start])
		output.WriteString(span.value)
		start = span.end
	}
	output.WriteString(p.input[start:])
	return output.String(), hits
}

func (r *Redactor) execute(input, kind string, collect bool) (string, []Hit) {
	if r.disabled {
		return input, []Hit{}
	}
	plan := newRedactionPlan(r, input)
	switch kind {
	case "url":
		plan.custom(plan.root())
		plan.url(plan.root())
	case "json", "headers":
		if json.Valid([]byte(input)) {
			index := 0
			node := parseJSONNode(input, &index)
			plan.body(node, kind == "headers")
		} else {
			plan.text(plan.root())
		}
	default:
		plan.text(plan.root())
	}
	return plan.finish(collect)
}

type jsonField struct {
	name string
	node *jsonNode
}

type jsonNode struct {
	start, end int
	kind       byte
	fields     []jsonField
	children   []*jsonNode
}

func skipJSONSpace(input string, index *int) {
	for *index < len(input) && strings.ContainsRune(" \r\n\t", rune(input[*index])) {
		*index++
	}
}

func stringEnd(input string, start int) int {
	for index := start + 1; index < len(input); index++ {
		if input[index] == '\\' {
			index++
		} else if input[index] == '"' {
			return index + 1
		}
	}
	return len(input)
}

// The standard parser has already validated input and enforced nesting limits.
func parseJSONNode(input string, index *int) *jsonNode {
	skipJSONSpace(input, index)
	node := &jsonNode{start: *index, kind: input[*index]}
	switch node.kind {
	case '{', '[':
		*index++
		skipJSONSpace(input, index)
		for input[*index] != '}' && input[*index] != ']' {
			if node.kind == '{' {
				end := stringEnd(input, *index)
				var name string
				_ = json.Unmarshal([]byte(input[*index:end]), &name)
				*index = end
				skipJSONSpace(input, index)
				*index++ // colon
				node.fields = append(node.fields, jsonField{name, parseJSONNode(input, index)})
			} else {
				node.children = append(node.children, parseJSONNode(input, index))
			}
			skipJSONSpace(input, index)
			if input[*index] == ',' {
				*index++
				skipJSONSpace(input, index)
			}
		}
		*index++
	case '"':
		*index = stringEnd(input, *index)
	default:
		for *index < len(input) && !strings.ContainsRune(" \r\n\t,]}", rune(input[*index])) {
			*index++
		}
	}
	node.end = *index
	return node
}

func (p *redactionPlan) maskNode(node *jsonNode, sources []string) {
	if len(sources) == 0 || node.kind == 'n' {
		return
	}
	if node.kind == '"' {
		unit := p.root().decodeJSON(node.start, node.end)
		for _, id := range sources {
			p.mask(unit, id)
		}
		return
	}
	if (node.kind == '{' || node.kind == '[') && !p.root().allows(node.start, node.end) {
		// Keep pre-existing markers inside an object, while still masking all
		// fresh values covered by its sensitive parent field.
		before := len(p.spans)
		for _, field := range node.fields {
			p.maskNode(field.node, sources)
		}
		for _, child := range node.children {
			p.maskNode(child, sources)
		}
		for i := before; i < len(p.spans); i++ {
			p.spans[i].hitStart, p.spans[i].hitEnd = node.start, node.end
		}
		return
	}
	var compact bytes.Buffer
	_ = json.Compact(&compact, []byte(p.input[node.start:node.end]))
	secret := compact.String()
	for _, id := range sources {
		p.r.Register(id, secret)
		encoded, _ := json.Marshal(Mask(secret))
		p.add(p.root(), node.start, node.end, string(encoded), id)
	}
}

func (p *redactionPlan) body(node *jsonNode, headers bool) {
	switch node.kind {
	case '{':
		for _, field := range node.fields {
			p.body(field.node, headers)
			if headers {
				values := []*jsonNode{field.node}
				if field.node.kind == '[' {
					values = field.node.children
				}
				for _, value := range values {
					if value.kind == '"' {
						unit := p.root().decodeJSON(value.start, value.end)
						p.maskNode(value, p.r.registerHeader(field.name, unit.text))
					}
				}
			} else {
				p.maskNode(field.node, p.r.fieldSources(field.name, JSONFields))
			}
		}
	case '[':
		for _, child := range node.children {
			p.body(child, headers)
		}
	case '"':
		unit := p.root().decodeJSON(node.start, node.end)
		if headers {
			p.units = append(p.units, unit)
			p.custom(unit)
		} else {
			p.text(unit)
		}
	}
}
