import cv2
import json
import sys
import os

def parse_layout(image_path):
    if not os.path.exists(image_path):
        print(json.dumps({"error": f"Image file not found: {image_path}"}))
        sys.exit(1)

    # 1. Load image
    img = cv2.imread(image_path)
    height, width, _ = img.shape

    # 2. Preprocess (Gray -> Gaussian Blur -> Adaptive Threshold)
    gray = cv2.cvtColor(img, cv2.COLOR_BGR2GRAY)
    blur = cv2.GaussianBlur(gray, (5, 5), 0)
    thresh = cv2.adaptiveThreshold(
        blur, 255, cv2.ADAPTIVE_THRESH_GAUSSIAN_C, cv2.THRESH_BINARY_INV, 11, 2
    )

    # 3. Detect UI Component Contours
    contours, _ = cv2.findContours(thresh, cv2.RETR_EXTERNAL, cv2.CHAIN_APPROX_SIMPLE)
    
    elements = []
    for idx, cnt in enumerate(contours):
        x, y, w, h = cv2.boundingRect(cnt)
        
        # Filter out tiny noise elements or full-canvas borders
        if w < 10 or h < 10 or (w == width and h == height):
            continue

        # Classify element type roughly by aspect ratio and dimension boundaries
        aspect_ratio = float(w) / h
        if aspect_ratio > 5 and h < 50:
            elem_type = "navbar"
        elif aspect_ratio > 3 and h < 60:
            elem_type = "button"
        elif aspect_ratio > 1.5 and w > 400:
            elem_type = "card"
        else:
            elem_type = "container"

        elements.append({
            "id": idx,
            "type": elem_type,
            "bounds": {"x": x, "y": y, "w": w, "h": h},
            "text_content": ""  # Placeholder for local OCR/easyocr text ingestion
        })

    # Sort elements geometrically (top-to-bottom, left-to-right)
    elements.sort(key=lambda e: (e["bounds"]["y"], e["bounds"]["x"]))

    result = {
        "canvas_size": {"width": width, "height": height},
        "elements": elements
    }
    return result

if __name__ == "__main__":
    if len(sys.argv) < 2:
        print(json.dumps({"error": "Usage: python parser.py <image_path>"}))
        sys.exit(1)

    img_path = sys.argv[1]
    layout_data = parse_layout(img_path)
    print(json.dumps(layout_data, indent=2))
