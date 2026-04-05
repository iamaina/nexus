import fitz  # PyMuPDF
import sys
import json

def extract(path):
    doc = fitz.open(path)

    output = []

    for page_num, page in enumerate(doc, start=1):
        blocks = page.get_text("dict")["blocks"]

        for b in blocks:
            # IMAGE BLOCK
            if b["type"] == 1:
                output.append({
                    "type": "image",
                    "x": b["bbox"][0],
                    "y": b["bbox"][1],
                    "page": page_num
                })
                continue

            # TEXT BLOCK
            if "lines" not in b:
                continue

            for line in b["lines"]:
                for span in line["spans"]:
                    output.append({
                        "type": "text",
                        "text": span["text"],
                        "x": span["bbox"][0],
                        "y": span["bbox"][1],
                        "font_size": span["size"],
                        "font_name": span["font"],
                        "flags": span["flags"],
                        "page": page_num
                    })

    return output


if __name__ == "__main__":
    path = sys.argv[1]
    data = extract(path)
    print(json.dumps(data))
