# Native `.dd` package loader

DDGo's `internal/ddpackage` package reads Ghost Gunner `.dd` files as
read-only ZIP archives. It has no Qt, controller, transport, machine-readiness,
or execution dependency. Parsing a package does not make it safe or eligible to
run on a machine.

## Confirmed structure

A package must have one root-level, case-sensitive `manifest.yml` entry. Paths
in manifests are exact archive paths and use forward slashes. DDGo does not
infer file type from an extension: a `step_gcode` reference may name any
regular archive file. `Additional Files/` entries are separately discoverable,
but otherwise remain ordinary read-only resources.

The supported root manifest is a YAML sequence of job mappings. Each job
recognizes `job_name`, `job_text`, `job_steps`, `guide_files`, `model_files`,
`manifest_file`, `min_fw_version`, `min_ddcut_version`,
`disable_wcs_clear_prompt`, `wcs_value_check`, and
`wcs_check_failed_message`. Steps recognize `step_name`, `step_text`,
`step_markdown`, `step_image`, `step_gcode`, `pause`, `reset`, `timeout`,
`if`, `unskippable`, `step_goto`, `popup_text`, `popup_title`,
`popup_wait_time`, `popup_yes_step`, and `popup_no_step`.

Known operational metadata is retained only for a future guided UI. DDGo does
not execute timeout, reset, pause, condition, jump, or popup behavior in this
phase. Unknown mapping fields are preserved in package metadata and recorded
as compatibility warnings. Literal `\\n` is interpreted as a newline only in
documented human-text fields; resource paths, G-code, and Markdown are not
rewritten. YAML block Markdown is retained verbatim by the YAML parser.

## Submanifests

`manifest_file` is accepted at job level and as a `job_steps` list item. A
submanifest is a YAML sequence of step mappings; its steps are inserted at the
reference position. Nested and repeated noncyclic includes are supported.
Every resulting step retains its source manifest path, while its stable package
step ID reflects the final job order. DDGo detects missing references and
cycles, and bounds both include depth and total expanded steps.

The GG3 manual describes submanifests as step lists without job headers. The
public DDCut implementation also has a job-level `manifest_file` path; DDGo
supports both as ordered step insertion. No control-flow directive is acted on
while expanding an include.

## Safety and resource lifetime

Archive entries are checked before parsing. DDGo rejects duplicate paths,
absolute/traversal/drive/UNC paths, **any single backslash**, NUL names,
symbolic links, and special files. It permits harmless directory entries.
Referenced G-code,
images, guides, models, and submanifests must exist with exact case.

Default limits are centralized in `ddpackage.DefaultLimits()`:

- 4,096 archive entries;
- 1 MiB manifest, 32 MiB per resource, and 128 MiB total decompressed data;
- 200:1 declared compression ratio;
- 16 nested include levels and 10,000 expanded steps.

Read data is bounded by the actual bytes produced while decompressing, not just
ZIP metadata. The loader snapshots all validated resource bytes into bounded
memory and closes the ZIP immediately. `ReadResource` returns a copy, so a
subsequent on-disk replacement cannot alter a loaded package or a caller's
previous resource data.

`MaxExpandedSteps` applies to the combined final output of every job in one
package. Every occurrence of a repeated submanifest counts again. The manifest
limit applies to root `manifest.yml` and to every resource decoded through
`manifest_file`, regardless of that resource's extension. Ordinary resources
retain the separate per-resource allowance; an otherwise valid large G-code or
model file is not treated as a manifest. Total decompressed-size and exact
compression-ratio checks continue to apply to all resources.

## Compatibility scope and future integration

The manual calls the root file `[Link]` in prose but examples and public DDCut
use `manifest.yml`; DDGo requires the latter exactly. Standard YAML is always
parsed first with YAML v3. When that strict parse fails, DDGo has a bounded,
line-aware fallback for a small set of observed historic DDCut conventions:

- literal `step_markdown: |` content that starts at column zero, ending at a
  structural boundary in its containing step/job (including an unknown mapping
  field, a later step, or a later job);
- tabs on otherwise blank or comment-only lines;
- unquoted colon-space text in the documented human-text fields (`job_name`,
  `job_text`, `step_name`, `step_text`, popup text/title, and the WCS failure
  message); and
- an extra space after a sequence marker on a mapping item.

The fallback is not a permissive YAML replacement. It rejects tabs used for
actual YAML indentation, leaves resource paths and numeric fields alone, and
returns an error when it cannot produce one strict YAML document or safely
classify a legacy Markdown boundary. Correctly indented literal and folded
block scalars are preserved without line-level compatibility rewriting, even
when another field elsewhere requires fallback. Unknown job and step metadata
continues to be retained rather than becoming Markdown content. Recovered
manifests receive concise nonfatal compatibility diagnostics. The archive's
original manifest bytes are never rewritten; only a bounded in-memory parsing
buffer is supplied to YAML v3 after strict decoding failed. That normalized
buffer is capped at twice the configured per-manifest byte limit; each expanded
line is checked before indentation, quoting, or output construction. Root
manifests and submanifests use the same policy and remain subject to the
existing manifest, resource, archive, inclusion-depth, and expanded-step
limits.

Initial loader development had no real-world fixtures and therefore used only
synthetic archives. A later compatibility investigation validated manifest
structure against one locally supplied legacy package; this does not claim
universal compatibility with every historic `.dd` dialect or archive layout.
The legacy C++ parser has custom YAML behavior; DDGo intentionally recognizes
only the documented deviations above rather than emulating arbitrary malformed
syntax.

Reference material reviewed: [DDCutPublic](https://github.com/Defdist/DDCutPublic)
(`Archive`, `Manifest`, `Job`, `Operation`, and `DDFile`) and the
[GG3 Operator's Manual](https://www.scribd.com/document/876842787/LATEST-GG3-Operators-Manual),
chapter 19. The public source uses a custom YAML parser and historically treats
the first parsed document as the root manifest; DDGo instead requires one
standard-YAML document with an explicit sequence root.

DDGo now presents a loaded package as one first-class package
document. It browses manifest-order jobs and steps, preserves text/Markdown,
shows bounded in-memory images when Qt can decode them, lists resource
references and complete path/field/message loader diagnostics, and can open a
supplementary G-code view for any `step_gcode`. These views use the retained
archive snapshot without extracting resources, provide read-only source
display plus execution controls, progress, and status for their associated
step session, and provide Back to Package navigation. Closing an auxiliary
view does not close the package; accepted package closure closes its auxiliary
views.

DDGo can manually run one step containing valid G-code from either the package
window or that step's auxiliary view. This is an exact package-document,
step-session, and controller-RunID operation; it is not a Run Entire Package
operation. DDGo does not advance a guided workflow, evaluate conditions,
display models externally, or act on pause/reset/timeout/jump/popup metadata.
Those fields remain retained for future guided-workflow support and are not
implied by manually running a step.
