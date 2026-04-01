package layout

import "strings"

func RenderBlock(b Block, prefix string) []string {
	var lines []string

	switch b.Type {

	case BlockParagraph:
		lines = append(lines, prefix+b.Text)

	case BlockCode:
		lines = append(lines, prefix+"[code]")
		for _, l := range strings.Split(b.Text, "\n") {
			lines = append(lines, prefix+"  "+l)
		}

	case BlockImage:
		if b.Caption != "" {
			lines = append(lines, prefix+"[image: "+b.Caption+"]")
		} else {
			lines = append(lines, prefix+"[image]")
		}

	case BlockList:
		for _, item := range b.Items {
			lines = append(lines, prefix+"- "+item)
		}
	}

	return lines
}
