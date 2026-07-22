# Clueso Editor UI Reference Archive

## Purpose And Boundary

This file records only verified, user-supplied observations of the Clueso
editor UI. It is a design research archive, not a product requirement or an
implementation source.

Do not copy Clueso HTML, CSS, SVG assets, JavaScript bundles, fonts, external
URLs, tokens, cookies, or temporary media references into this repository.
Our editor must preserve the Server-side evidence, source-material, and
approval boundaries defined by the existing architecture and edit-plan
contracts.

## Evidence Register

| ID | Source | Reliability | Permitted Use |
| --- | --- | --- | --- |
| CLU-001 | User-provided desktop editor screenshot | High for visible layout and labels in that state | Visual reference |
| CLU-002 | User-provided left toolbar DOM capture | High for toolbar button order, labels, SVG presence, and selected state | Component inventory |
| CLU-003 | User-provided runtime DOM export from the live editor | Medium for static DOM/classes; low for behavior and offline rendering | Structural clues only |
| CLU-004 | User-provided HTML saved by browser | Low for offline visual fidelity | Resource/class discovery only |
| CLU-005 | Sanitized local CDP capture: `tools-captions-open-v2` | High for the visible desktop Captions panel and its geometry | Visual/component reference |
| CLU-006 | Sanitized local CDP capture: `aspect-ratio-menu-open` | High for the visible aspect-ratio dropdown and its options | Visual/component reference |
| CLU-007 | Sanitized local CDP capture: `timeline-video-selected` | High for the visible `Video` property-panel structure | Visual/component reference |

The HTML exports are snapshots of a React runtime. They are not Clueso source
code and must not be treated as a runnable editor implementation.

## Confirmed Desktop Layout

The observed desktop editor has six visible regions:

1. Project top bar: project title, Video/Article mode switch, account/support,
   translation, and share actions.
2. Left vertical tool rail.
3. Transcript/script panel with search, clip headers, editable text, AI rewrite,
   and speech-generation actions.
4. Central canvas for a title/scene composition with a zoom control.
5. Right project/property panel with transcript visibility and aspect-ratio
   settings in the observed state.
6. Bottom timeline with transport controls, timecode, speed/quality controls,
   track lanes, and clip thumbnails.

This confirms that Clueso represents a video as more than a sequence of raw
video clips: it also has editable presentation scenes and transcript-driven
controls. It does not establish the underlying data schema or behavior.

## Confirmed Left Tool Rail

The following buttons appear in this exact top-to-bottom order:

| Order | Accessible Label | Confirmed State |
| --- | --- | --- |
| 1 | Select | Active in the captured state; inverted background/stroke |
| 2 | Assets | Standard tool button |
| 3 | Text | Standard tool button |
| 4 | Shapes | Menu trigger (`aria-haspopup="menu"`) |
| 5 | Effects | Standard tool button |
| 6 | Animation | Standard tool button |
| 7 | Template | Standard tool button |
| 8 | Transition | Standard tool button |
| 9 | Captions | Standard tool button |
| 10 | Comments | Standard tool button |

Observed shared characteristics:

- Semantic `button` controls with `aria-label` values and tooltip-trigger
  markers.
- A vertical flex layout with centered icon buttons, small padding, rounded
  corners, and color transitions.
- The selected Select control uses an inverted visual treatment; the others
  use a normal-stroke treatment.
- The captured toolbar includes SVG icons, but no Clueso SVG should be copied
  into this project.

## Confirmed Captions Panel

The sanitized capture `tools-captions-open-v2` confirms that selecting the
Captions rail control opens a left-side library panel without replacing the
central canvas or timeline. In the observed state, the panel contains:

- a `Captions` heading and close control;
- `Saved` and `Library` selection controls plus search;
- a caption-style collection with visual previews and style names;
- a bottom `Download` action and a `Visibility` toggle.

The visual state also shows that the Select rail control can retain its
selection treatment while the Captions panel is open. Our editor should model
the current canvas tool separately from an open library/property panel; a
single mutually-exclusive "active tool" flag would not match this behavior.

The capture establishes layout and visible controls only. It does not establish
what applying a caption style changes, whether `Download` is an asset download
or an export, or the persistence semantics of `Visibility`.

## Confirmed Aspect-Ratio Menu

The sanitized capture `aspect-ratio-menu-open` confirms that aspect ratio is a
project-level setting in the right `Project` panel, independent of the open
Captions panel. It is positioned below the transcript visibility setting.

The observed dropdown presents the current value and at least these visible
ratio choices:

- `Original (1920x1080)`;
- `Landscape 16:9` (selected in the observed state);
- `Portrait 9:16`;
- `Square 1:1`;
- `Standard 4:3`;
- `Portrait 3:4`.

This is evidence for an explicit project/output canvas ratio control. It does
not show how an existing timeline is reframed, whether assets are cropped or
letterboxed, or whether a ratio change is undoable.

## Confirmed Video Property Panel

The sanitized `timeline-video-selected` capture shows a right-side property
panel headed `Video`, with sibling `Properties` and `Audio` tabs. The visible
Properties tab groups controls into at least:

- background selection and replacement;
- a background preview and color-related control;
- an "apply to all clips" toggle and a default-setting action;
- layout/alignment controls.

This supports a selection-driven inspector in our own editor, with separate
visual and audio configuration. The capture does not establish whether the
currently selected object is a raw media clip, a scene rectangle, or a
background layer, so it must not be used to infer a data model or edit scope.

## Runtime DOM Findings

The live DOM export contains the following useful structural clues:

- `EditorPlatform`, `TextEditorPlatform`, `VideoTranscriptClipHeader`,
  `TimelineCanvasInner`, and video timeline container class names.
- A scrollable timeline container and a timeline resize-handle element.
- Transcript styles for playing/paused word highlighting, busy overlays, search
  highlighting, and sync markers.
- Static evidence of 80 buttons, 87 SVG elements, 3 canvas elements, and no
  HTML `video` element in the captured state.

The absence of a `video` element is not a defect. It suggests that preview
frames or parts of the timeline may be canvas-rendered. The snapshot alone
does not prove the rendering pipeline.

## Export Limitations And Offline Failure

The console export starts at the `html` element and does not include a doctype.
More importantly, it references many root-relative and external resources,
including stylesheet and JavaScript paths such as `/assets/...`.

When opened through `file://`, those root-relative paths no longer resolve to
the original site. React event handlers and in-memory state also are not
serialized as inline HTML handlers. Therefore:

- A local opening that looks broken is expected.
- The export must not be used to assess Clueso's production quality.
- The export is unsuitable as a reusable page template.
- A static HTML snapshot cannot reproduce drag, playback, save, export, or
  transcript synchronization behavior.

## Candidate Mapping To Our Editor

Potentially relevant concepts for future design study:

- A compact, accessible vertical tool rail.
- Transcript-aware captions and narration workflows.
- Explicit project aspect-ratio settings.
- Presentation scenes for title, logo, and explanatory material.
- A timeline that distinguishes source video from presentation layers.

These are not automatically approved features. In particular, any future
effects, animation, transition, template, or AI action must remain constrained
by our `DemoEditPlan`, source-material policy, required-step order, and manual
approval rules. Step screenshots remain `presentation_only` and cannot become
business-step evidence.

## Open Questions

The following must be captured from the live desktop page before treating a
Clueso interaction as a requirement:

1. What panel, menu, or canvas mode opens after each tool-rail button click.
2. Hover, focus, active, disabled, and tooltip behavior for each button.
3. Computed styles and box-model values for the top bar, tool rail, transcript
   panel, canvas, right panel, timeline, and a selected clip.
4. Transcript-to-timeline synchronization behavior, including text edits.
5. Timeline drag, split, trim, resize, zoom, and track visibility behavior.
6. Right-panel contents for selected video, text, caption, image, audio, and
   effect objects.
7. Responsive breakpoints. The page intentionally presents a desktop-only
   fallback when the viewport is too narrow.
8. Caption-style application, visibility, download, and timeline behavior.
9. Aspect-ratio change behavior, including crop/fit policy and undo history.

## Required Future Capture Package

For each new observed state, retain only sanitized artifacts:

```text
clueso-study/
  screenshots/<state>.png
  recordings/<workflow>.mp4
  components/<state>-outer-html.txt
  components/<state>-computed-styles.json
  workflow-notes.md
  network-sanitized.har
```

Never store cookies, authorization headers, API keys, personal data, signed
media URLs, or copied proprietary bundles in the repository.
