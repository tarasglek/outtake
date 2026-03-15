package gmail

import (
	"net/mail"
	"sort"
	"strings"
)

func readLabelsFromMaildirMessage(msg *mail.Message) ([]string, error) {
	if msg == nil {
		return nil, nil
	}

	rawValues := msg.Header[labelsHeader]
	if len(rawValues) == 0 {
		for k, vals := range msg.Header {
			if strings.EqualFold(k, labelsHeader) {
				rawValues = append(rawValues, vals...)
			}
		}
	}

	labels := make([]string, 0, len(rawValues))
	for _, v := range rawValues {
		for _, piece := range strings.Split(v, ",") {
			p := strings.TrimSpace(piece)
			if p == "" {
				continue
			}
			labels = append(labels, p)
		}
	}
	return normalizeLabels(labels), nil
}

func normalizeLabels(labels []string) []string {
	seen := make(map[string]struct{}, len(labels))
	out := make([]string, 0, len(labels))
	for _, l := range labels {
		t := strings.TrimSpace(l)
		if t == "" {
			continue
		}
		if _, ok := seen[t]; ok {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}
