name	drawio-custom-flowchart
description	Generate a standardized custom Draw.io XML flow diagram representing business logic and project structure, following specific layout, color scheme, and diagram styles.
license	MIT
metadata	
author	version
user-custom
1.0

This skill provides instructions for generating standardized, high-quality Draw.io XML diagrams representing business logic, project structure, or flowchart workflows in the workspace.

Style Guidelines
When creating or modifying Draw.io XML diagrams with this skill, strictly adhere to the following stylistic guidelines:

Page & Canvas Settings
Page Size: 1654 width by 1169 height (A3 Landscape equivalent). Specify pageWidth="1654" pageHeight="1169" on the <mxGraphModel> element.
Adaptive Colors: Add adaptiveColors="auto" to the <mxGraphModel> element to support automatic light/dark mode adaptation.
Layout Direction: Top-to-bottom layout.

Fonts & Typography
Diagram Title: Bold, font size 18, color #1a237e. Positioned prominently at the top.
Standard Nodes: Font size 12.
Detail/Reference Boxes: Font size 11.
Labels: Always include html=1 in the style string and XML-escape HTML characters (e.g. <b> as &lt;b&gt;) for rich text formatting.

Node Shapes & Color Palette
Use these precise HSL-harmonized colors and shapes:

Category	Shape	Fill Color (fillColor)	Stroke Color (strokeColor)	Text Style (fontStyle)
Start/End	Ellipse	#dae8fc (Blue-gray)	#6c8ebf (Medium Blue-gray)	fontStyle=1 (Bold)
Decisions	Rhombus	#fff2cc (Soft Yellow)	#d6b656 (Dark Gold)	Regular
Process	Rounded Rect	#dae8fc (Soft Blue)	#6c8ebf (Medium Blue)	Regular
Problems	Rounded Rect	#ffe6cc (Soft Orange)	#d6b656 (Dark Gold)	Regular
Impact	Rounded Rect	#f8cecc (Soft Red)	#b85450 (Medium Red)	Regular
Solutions	Rounded Rect	#d5e8d4 (Soft Green)	#82b366 (Medium Green)	Regular
Reference	Rounded Rect	#e1d5e7 (Soft Purple)	#9673a6 (Medium Purple)	Regular

Layout & Placement Grid
Follow a logical coordinate system to avoid overlapping:

Columns: x = col_index * 220 + 80 (e.g., Col 0 = 80, Col 1 = 300, Col 2 = 520...)
Rows: y = row_index * 150 + 120 (e.g., Row 0 = 120, Row 1 = 270, Row 2 = 420...)
Default Node Dimensions:
Rounded Rectangles: width="160" height="80"
Diamonds (Rhombus): width="140" height="100"
Ellipses: width="100" height="70"

Connectors (Edges)
Style: Always use orthogonal routing.
Style String: style="edgeStyle=orthogonalEdgeStyle;rounded=1;html=1;fontSize=12;strokeColor=#555555;"
Rule: Every connector must be an mxCell with a nested mxGeometry element having relative="1" as="geometry".

Legend & Scope Boxes
Add these boxes to the bottom-left of the canvas:

Legend Box:
Location: Bottom-left corner (e.g., x="50" y="800" width="280" height="220").
Contains colored swatches corresponding to the node categories above (Process, Problems, Impact, Solutions, etc.) to act as a map for the reader.

Scope Box:
Location: Directly below the legend box (e.g., x="50" y="1040" width="280" height="70").
Contains metadata about the diagram (e.g., author, date, scope: "CodeArena Project Structure & Business Logic").

Saving & Version Control
Format: Always save the final XML content with the .drawio file extension (e.g., architecture.drawio).
Location: Write the .drawio file directly to the workspace (such as the project root or a docs/ directory) using the file creation tools.
Why: Saving diagrams in the repository ensures they are version-controlled, can be reviewed in pull requests, and can be opened/edited locally or via the Draw.io integration.