// render() is the only place that touches the DOM. It is called once on
// load and again every time state.js emits "change" / "jobChange".
// It never runs on a timer and never runs from inside app.js's event handlers directly.

// Mirrors `requiredVariants` in cmd/api/imageporcessing.go. Used to show
// "Pending" placeholder cards while a job runs, and to order finished cards.
const EXPECTED_VARIANTS = [
  { name: "thumbnail", label: "Thumbnail", dims: "150 \u00d7 150" },
  { name: "preview", label: "Preview", dims: "Up to 800 \u00d7 600" },
  { name: "display", label: "Display", dims: "Up to 1200 \u00d7 900" },
];

const STATUS_META = {
  queued: { label: "Queued", icon: "clock" },
  processing: { label: "Processing", icon: "loader", spin: true },
  completed: { label: "Completed", icon: "circle-check" },
  failed: { label: "Failed", icon: "x-circle" },
};

// [V2] human-readable names for the persisted `stage` values.
const STAGE_LABELS = {
  queued: "Waiting in queue",
  starting: "Starting",
  thumbnail_ready: "Thumbnail ready",
  preview_ready: "Preview ready",
  display_ready: "Display ready",
};
const MODE_LABELS = {
  short: "Short polling (1 s)",
  long: "Long polling (held up to 20 s)",
};

const SVG_NS = "http://www.w3.org/2000/svg";

const dom = {
  dropzone: document.getElementById("dropzone"),
  emptyState: document.getElementById("empty-state"),
  previewState: document.getElementById("preview-state"),
  previewImage: document.getElementById("preview-image"),
  previewName: document.getElementById("preview-name"),
  previewTags: document.getElementById("preview-tags"),
  chooseButtons: document.querySelectorAll("[data-choose]"),
  processButton: document.getElementById("process-button"),
  processIcon: document.getElementById("process-icon"),
  processLabel: document.getElementById("process-label"),
  uploadError: document.getElementById("upload-error"),
  uploadErrorText: document.getElementById("upload-error-text"),

  jobEmpty: document.getElementById("job-empty"),
  jobActive: document.getElementById("job-active"),
  jobStrip: document.getElementById("job-strip"),
  jobIdLabel: document.getElementById("job-id-label"),
  jobStatusBadge: document.getElementById("job-status-badge"),
  jobAside: document.getElementById("job-aside"),
  pollingIndicator: document.getElementById("polling-indicator"),
  jobRetrievalError: document.getElementById("job-retrieval-error"),
  // new elements
  modeRadios: document.querySelectorAll('input[name="observation-mode"]'),
  jobModeLabel: document.getElementById("job-mode-label"),
  jobVersionLabel: document.getElementById("job-version-label"),
  jobStageLabel: document.getElementById("job-stage-label"),
  jobProgress: document.getElementById("job-progress"),
  jobProgressText: document.getElementById("job-progress-text"),
  jobSimLabel: document.getElementById("job-sim-label"),
  jobSimStep: document.getElementById("job-sim-step"),
  pollingIndicatorText: document.getElementById("polling-indicator-text"),
  observationStopped: document.getElementById("job-observation-stopped"),
  experimentStats: document.getElementById("experiment-stats"),
  timeline: {
    accepted: document.getElementById("timeline-accepted"),
    stored: document.getElementById("timeline-stored"),
    generating: document.getElementById("timeline-generating"),
    complete: document.getElementById("timeline-complete"),
  },

  resultsEmpty: document.getElementById("results-empty"),
  resultsFailed: document.getElementById("results-failed"),
  resultsFailedMessage: document.getElementById("results-failed-message"),
  resultsGrid: document.getElementById("results-grid"),
};

/* ---------- small helpers ---------- */

function icon(name, extraClass) {
  const svg = document.createElementNS(SVG_NS, "svg");
  svg.setAttribute("class", extraClass ? `ic ${extraClass}` : "ic");
  svg.setAttribute("aria-hidden", "true");
  const use = document.createElementNS(SVG_NS, "use");
  use.setAttribute("href", `#i-${name}`);
  svg.appendChild(use);
  return svg;
}

function el(tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}

function formatBytes(bytes) {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

function formatTime(iso) {
  if (!iso) return "";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  return d.toLocaleTimeString([], { hour: "numeric", minute: "2-digit", second: "2-digit" });
}

// The API's error messages are lowercase fragments ("the image could not be processed");
// present them as a sentence.
function sentenceCase(text) {
  if (!text) return "";
  const t = text.trim();
  return t.charAt(0).toUpperCase() + t.slice(1) + (/[.!?]$/.test(t) ? "" : ".");
}

function formatFileType(mime) {
  if (mime === "image/jpeg") return "JPEG";
  if (mime === "image/png") return "PNG";
  return mime || "unknown";
}

function render(current, job) {
  renderModeSelector(current); // [V2]
  renderUploadCard(current);
  renderJobCard(job);
  renderResultsCard(job);
}

/* ---------- observation mode selector ---------- */
function renderModeSelector(current) {
  dom.modeRadios.forEach((radio) => {
    radio.checked = radio.value === current.observationMode;
  });
}

/* ---------- upload card ---------- */

function renderUploadCard(current) {
  const hasSelection = current.phase !== UploadPhase.IDLE && current.selectedFile;
  const uploading = current.phase === UploadPhase.UPLOADING;

  dom.dropzone.classList.toggle("is-dragover", current.isDragging);
  dom.emptyState.hidden = !!hasSelection;
  dom.previewState.hidden = !hasSelection;

  if (hasSelection) {
    if (dom.previewImage.getAttribute("src") !== current.previewUrl) {
      dom.previewImage.src = current.previewUrl;
    }
    dom.previewName.textContent = current.selectedFile.name;

    const parts = [formatBytes(current.selectedFile.size), formatFileType(current.selectedFile.type)];
    if (current.previewDimensions) {
      parts.push(`${current.previewDimensions.width} \u00d7 ${current.previewDimensions.height}`);
    }
    dom.previewTags.replaceChildren(...parts.map((t) => el("span", "tag", t)));
  }

  dom.chooseButtons.forEach((btn) => { btn.disabled = uploading; });

  // Disabled + relabeled while the POST is actually in flight.
  // Also disabled once ACCEPTED — state.selectedFile still points at the
  // File object that was just submitted, so a second click here would
  // silently resubmit the exact same bytes as a brand-new job unless the
  // user genuinely picks something new via "Choose another image" first.
  if (uploading) {
    dom.processButton.disabled = true;
    dom.processLabel.textContent = "Uploading\u2026";
    dom.processIcon.firstElementChild.setAttribute("href", "#i-loader");
    dom.processIcon.classList.add("ic--spin");
  } else {
    if (current.phase === UploadPhase.ACCEPTED) {
      dom.processButton.disabled = true;
      dom.processLabel.textContent = "Choose another image to process";
    } else {
      dom.processButton.disabled = !hasSelection;
      dom.processLabel.textContent = "Process image";
    }
    dom.processIcon.firstElementChild.setAttribute("href", "#i-upload");
    dom.processIcon.classList.remove("ic--spin");
  }

  if (current.phase === UploadPhase.ERROR && current.errorMessage) {
    dom.uploadError.hidden = false;
    dom.uploadErrorText.textContent = current.errorMessage;
  } else {
    dom.uploadError.hidden = true;
  }
}

/* ---------- job card ---------- */

function renderJobCard(job) {
  const hasJob = !!job.job;

  dom.jobEmpty.hidden = hasJob; // "No active job" only when there truly is none
  dom.jobActive.hidden = !hasJob;
  if (!hasJob) return;

  const j = job.job;
  const status = j.status;
  const isFailed = status === "failed";
  const isCompleted = status === "completed";
  const isTerminal = isCompleted || isFailed;

  dom.jobIdLabel.textContent = `#${j.id.slice(0, 8)}`;
  dom.jobStrip.dataset.status = status; // tints the whole strip (see .job-strip[data-status] in style.css)
  renderStatusBadge(status);

  // Timeline: the first two steps are always complete once a job exists.
  // The last two are active / pending / complete / error depending on status.
  setTimelineStep(dom.timeline.accepted, "complete", formatTime(j.queuedAt));
  setTimelineStep(dom.timeline.stored, "complete", formatTime(j.queuedAt));

  if (isCompleted) {
    setTimelineStep(dom.timeline.generating, "complete", formatTime(j.startedAt));
    setTimelineStep(dom.timeline.complete, "complete", formatTime(j.completedAt), "Complete");
  } else if (isFailed) {
    setTimelineStep(dom.timeline.generating, "error", formatTime(j.startedAt));
    setTimelineStep(dom.timeline.complete, "error", formatTime(j.completedAt), "Failed");
  } else {
    const waiting = status === "queued";
    setTimelineStep(dom.timeline.generating, "active", waiting ? "Waiting in queue" : formatTime(j.startedAt) || "In progress");
    setTimelineStep(dom.timeline.complete, "pending", "Pending", "Complete");
  }

  // mode, version, stage and progress (UI-01). Same code for both modes.
  dom.jobModeLabel.textContent = MODE_LABELS[job.mode] || job.mode;
  dom.jobVersionLabel.textContent = j.version != null ? String(j.version) : "";
  dom.jobStageLabel.textContent = STAGE_LABELS[j.stage] || j.stage || "";
  dom.jobProgress.value = j.variantsCompleted || 0; // REAL variants only
  dom.jobProgressText.textContent = `${j.variantsCompleted || 0} of 3`;
  // the simulated counter is labelled and appears only while the experiment is on.
  dom.jobSimLabel.hidden = !(j.simulationStep > 0) || isTerminal;
  dom.jobSimStep.textContent = String(j.simulationStep || 0);

  // Auto-refresh notice: only while a loop is running, and never
  // once the job has reached a terminal state (completed or failed).
  dom.pollingIndicator.hidden = !job.polling || isTerminal;
  dom.pollingIndicatorText.textContent =
    job.mode === "long"
      ? "Waiting for updates \u00b7 long polling (server holds each request up to 20 seconds)"
      : "Checking status automatically \u00b7 every 1 second";
  // shown after "Stop watching" (not a retrieval error)
  dom.observationStopped.hidden = !job.observationStopped || isTerminal;
  // Retrieval error is shown only after a failed GET, until the user clicks "Try again" or a new job starts.
  dom.jobRetrievalError.hidden = !job.retrievalError;
  dom.jobAside.hidden =
    dom.pollingIndicator.hidden && dom.jobRetrievalError.hidden && dom.observationStopped.hidden;

  // request counters for the experiment log
  const st = job.requestStats;
  dom.experimentStats.textContent = st
    ? [
        `requests started : ${st.started}`,
        `initial 200      : ${st.initial_200}`,
        `newer 200        : ${st.newer_200}`,
        `unchanged 200    : ${st.unchanged_200}`,
        `204 (no change)  : ${st.no_change_204}`,
        `errors           : ${st.errors}`,
        `cancelled        : ${st.cancelled}`,
        `pending          : ${st.pending}`,
      ].join("\n")
    : "No requests yet.";
}

// Only rebuilds the badge when the status actually changes, so the
// spinner animation isn't restarted on every poll.
function renderStatusBadge(status) {
  const badge = dom.jobStatusBadge;
  if (badge.dataset.status === status) return;

  const meta = STATUS_META[status] || { label: status, icon: "clock" };
  badge.dataset.status = status;
  badge.className = "status-badge";
  badge.replaceChildren(icon(meta.icon, meta.spin ? "ic--spin" : ""), el("span", "", meta.label));
}

// Sets a step's data-state ("complete" | "active" | "pending" | "error"),
// its sub-label (usually a time), and optionally its label text.
function setTimelineStep(stepEl, stepState, timeText, labelText) {
  stepEl.dataset.state = stepState;
  stepEl.querySelector(".tl-time").textContent = timeText || "";
  if (labelText !== undefined) stepEl.querySelector(".tl-label").textContent = labelText;
}

/* ---------- results card ---------- */

function renderResultsCard(job) {
  const status = job.job ? job.job.status : null;
  const isPending = status === "queued" || status === "processing";
  const isCompleted = status === "completed";

  // Mutually exclusive: empty / failed / grid (grid shows placeholders while pending, real cards when done).
  dom.resultsEmpty.hidden = status !== null;
  dom.resultsFailed.hidden = status !== "failed";
  dom.resultsGrid.hidden = !(isPending || isCompleted);

  if (status === "failed") {
    dom.resultsFailedMessage.textContent = sentenceCase(job.job.error) || "The image could not be processed.";
  }

  // Rebuild the grid only when the job or mode changes, so images don't
  // reload and shimmer animations don't restart on every poll.
  const mode = isCompleted ? "ready" : isPending ? "pending" : "";
  const key = mode ? `${mode}:${job.job.id}` : "";
  if (dom.resultsGrid.dataset.key === key) return;
  dom.resultsGrid.dataset.key = key;

  if (mode === "ready") {
    renderVariants(job.job.variants, job.source);
  } else if (mode === "pending") {
    renderPendingVariants();
  } else {
    // Never leave a previous job's variant cards in the DOM.
    dom.resultsGrid.replaceChildren();
  }
}

// Every card is laid out the same way: image (in a matte) on top,
// then name + status, dimensions, and actions underneath.
function variantCard(kind, label, dims, statusChip, matNode) {
  const card = el("article", `variant-card variant-card--${kind}`);
  card.appendChild(matNode);

  const info = el("div", "variant-info");
  const head = el("div", "variant-head");
  head.appendChild(el("h3", "variant-name", label));
  head.appendChild(statusChip);
  info.appendChild(head);
  info.appendChild(el("p", "variant-dims", dims));

  const actions = el("div", "variant-actions");
  info.appendChild(actions);
  card.appendChild(info);
  return { card, actions };
}

function chip(kind, iconName, text) {
  const c = el("span", `chip chip--${kind}`);
  c.appendChild(icon(iconName));
  c.appendChild(document.createTextNode(text));
  return c;
}

function actionButton(href, iconName, text, kind, attrs) {
  const a = el("a", `btn btn--sm btn--${kind}`);
  if (href) a.href = href;
  for (const [k, v] of Object.entries(attrs || {})) a.setAttribute(k, v);
  a.appendChild(icon(iconName));
  a.appendChild(document.createTextNode(text));
  return a;
}

// Placeholder cards shown while the job is queued or processing.
function renderPendingVariants() {
  const cards = EXPECTED_VARIANTS.map((v) => {
    const mat = el("div", "variant-mat variant-mat--pending");
    mat.appendChild(icon("image"));

    const { card, actions } = variantCard("pending", v.label, v.dims, chip("pending", "clock", "Pending"), mat);
    actions.appendChild(actionButton(null, "external-link", "View", "ghost", { "aria-disabled": "true", tabindex: "-1" }));
    actions.appendChild(actionButton(null, "download", "Download", "ghost", { "aria-disabled": "true", tabindex: "-1" }));
    return card;
  });
  dom.resultsGrid.replaceChildren(...cards);
}

// Grid of finished variant cards.
function renderVariants(variants, source) {
  const rank = (name) => {
    const i = EXPECTED_VARIANTS.findIndex((v) => v.name === name);
    return i === -1 ? EXPECTED_VARIANTS.length : i;
  };
  const sorted = [...(variants || [])].sort((a, b) => rank(a.name) - rank(b.name));

  const cards = sorted.map((variant) => {
    const meta = EXPECTED_VARIANTS.find((v) => v.name === variant.name);
    const label = meta ? meta.label : variant.name;

    const mat = el("a", "variant-mat");
    mat.href = variant.url;
    mat.target = "_blank";
    mat.rel = "noopener";
    mat.setAttribute("aria-label", `Open ${label} variant`);
    const img = document.createElement("img");
    img.src = variant.url;
    img.alt = `${label} variant`;
    img.loading = "lazy";
    mat.appendChild(img);

    const { card, actions } = variantCard(
      "ready",
      label,
      `${variant.width} \u00d7 ${variant.height}`,
      chip("ready", "circle-check", "Ready"),
      mat
    );
    actions.appendChild(actionButton(variant.url, "external-link", "View", "ghost", { target: "_blank", rel: "noopener" }));
    actions.appendChild(actionButton(variant.url, "download", "Download", "primary", { download: downloadName(variant, source) }));
    return card;
  });

  dom.resultsGrid.replaceChildren(...cards);
}

// e.g. "belize-coast.jpg" + "thumbnail" -> "belize-coast-thumbnail.jpg"
// (variant URLs have no file extension, so derive it from the uploaded file's type)
function downloadName(variant, source) {
  const original = (source && source.name) || "image";
  const base = original.replace(/\.[^.]+$/, "").replace(/[^\w.-]+/g, "-") || "image";
  const ext = source && source.mediaType === "image/png" ? "png" : "jpg";
  return `${base}-${variant.name}.${ext}`;
}