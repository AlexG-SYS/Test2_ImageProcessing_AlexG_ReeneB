const ACCEPTED_TYPES = ["image/jpeg", "image/png"];
const MAX_SIZE_BYTES = 10 * 1024 * 1024;
const POLL_INTERVAL_MS = 1000; // short polling: ~1 s DELAY AFTER each response (not start-to-start); never overlaps
const POLL_TIMEOUT_MS = 8000; // short polling per-request timeout
const LONG_POLL_WAIT_SECONDS = 20; // server holding deadline requested with ?wait=
const LONG_POLL_TIMEOUT_MS = 30000; // browser request timeout: 30 s, longer than the 20 s hold

function handleFileSelected(file) {
  if (!file) return;
  if (state.isSubmitting) return; // don't swap the file out from under an in-flight upload

  // validate the file type and size before proceeding. If the file is invalid, set the state to ERROR with an appropriate message.
  if (!ACCEPTED_TYPES.includes(file.type)) {
    setState({ phase: UploadPhase.ERROR, errorMessage: "Only JPEG and PNG images are supported." });
    return;
  }
  if (file.size > MAX_SIZE_BYTES) {
    setState({ phase: UploadPhase.ERROR, errorMessage: "Image must not exceed 10 MB." });
    return;
  }

  if (state.previewUrl) URL.revokeObjectURL(state.previewUrl);

  // If the file is valid, update the state to SELECTED, store the selected file, and create a preview URL for display.
  const previewUrl = URL.createObjectURL(file);
  setState({
    phase: UploadPhase.SELECTED,
    selectedFile: file,
    previewUrl,
    previewDimensions: null,
    errorMessage: null,
  });

  // Read the pixel dimensions once the browser has decoded the preview.
  // Ignored if the user has already picked a different file by then.
  const probe = new Image();
  probe.onload = () => {
    if (state.previewUrl !== previewUrl) return;
    setState({ previewDimensions: { width: probe.naturalWidth, height: probe.naturalHeight } });
  };
  probe.src = previewUrl;
}

async function handleProcessClick() {
  // Prevent multiple submissions and ensure a file is selected before proceeding. 
  // If the state indicates that a submission is already in progress or no file is selected, exit early.
  if (state.isSubmitting) return;
  if (!state.selectedFile) return;

  state.isSubmitting = true; // set synchronously, before the button is even disabled

  // Start fresh: stop polling the previous job and clear its job card and
  // variant results before the new upload begins.
  stopObserving();
  resetJobState();

  setState({ phase: UploadPhase.UPLOADING, errorMessage: null });

  try {
    const result = await submitImage(state.selectedFile);
    setState({ phase: UploadPhase.ACCEPTED, acceptedImage: result });
    startObservingJob(result);
  } catch (err) {
    // If an error occurs during the submission process, update the state to ERROR and display the error message to the user.
    setState({ phase: UploadPhase.ERROR, errorMessage: err.message });
  } finally {
    state.isSubmitting = false;
  }
}

// Observation loop: one GET per poll, with a timeout. The loop is cancellable by the user (Stop watching) or by a new upload/mode switch. 
// The loop is also cancelled when the job reaches a terminal state (completed/failed).
let observationController = null; // AbortController of the loop that is currently running
let observationGeneration = 0; // bumped on every stop; a loop whose token is stale must do nothing

// Raw client log for the experiments: one entry per observation GET STARTED.
let runLog = newRunLog(null);

function newRunLog(jobId) {
  return { jobId, mode: null, image: null, baselineVersion: null, requests: [], finalJob: null };
}

// Cancels the running loop. The WORKER is untouched: only the browser stops asking.
function stopObserving() {
  observationGeneration++;
  if (observationController) {
    observationController.abort();
    observationController = null;
  }
}

// Starts observing an accepted job (called right after the 202).
function startObservingJob(accepted) {
  setJobState({
    job: {
      id: accepted.job_id,
      imageId: accepted.image_id,
      status: accepted.status,
      queuedAt: null,
      startedAt: null,
      completedAt: null,
      variants: [],
      error: null,
      //new fields; the first prompt snapshot fills them in
      version: accepted.version ?? 1,
      stage: "queued",
      variantsCompleted: 0,
      simulationStep: 0,
    },
    source: state.selectedFile
      ? { name: state.selectedFile.name, mediaType: state.selectedFile.type }
      : null,
    retrievalError: false,
  });

  beginObservation(accepted.job_id, { newRun: true });
}

// Starts (or restarts) the loop for a job in the CURRENTLY SELECTED mode.
// Used by: first observation, mode switch, Resume, Try again.
function beginObservation(jobId, { newRun }) {
  stopObserving(); // cancel any older loop and invalidate its late responses
  const generation = observationGeneration;
  const controller = new AbortController();
  observationController = controller;

  const mode = state.observationMode;
  if (newRun || runLog.jobId !== jobId) {
    runLog = newRunLog(jobId);
    runLog.image = state.selectedFile ? state.selectedFile.name : null;
  }
  runLog.mode = mode;

  setJobState({
    mode,
    polling: true,
    retrievalError: false,
    observationStopped: false,
    requestStats: computeStats(runLog.requests),
  });

  runObservation(jobId, mode, generation, controller);
}

// Wraps one observation GET with the mode's timeout and records it in runLog.
// A request that is aborted or times out is still accounted for (cancelled/error).
async function observeOnce(url, timeoutMs, kind, controller) {
  const entry = {
    seq: runLog.requests.length + 1,
    mode: runLog.mode,
    kind, // "initial" | "short" | "long"
    url,
    start_ms: Date.now(), // wall-clock: comparable with the server's commit_us / 1000
    receipt_ms: null,
    http_status: null,
    version: null,
    outcome: null, // initial_200 | newer_200 | unchanged_200 | no_change_204 | error | cancelled
    render_ms: null,
    error: null,
  };
  runLog.requests.push(entry);
  setJobState({ requestStats: computeStats(runLog.requests) });

  // Per-request timeout that also follows the loop's AbortController.
  const local = new AbortController();
  const timeoutId = setTimeout(() => local.abort(new Error("timeout")), timeoutMs);
  const onParentAbort = () => local.abort(controller.signal.reason);
  controller.signal.addEventListener("abort", onParentAbort, { once: true });

  try {
    const result = await fetchObservation(url, local.signal);
    entry.receipt_ms = Date.now();
    entry.http_status = result.status;
    entry.version = result.data ? result.data.version : null;
    return { entry, result };
  } catch (err) {
    entry.receipt_ms = Date.now();
    entry.outcome = controller.signal.aborted ? "cancelled" : "error";
    entry.error = String((err && err.message) || err);
    throw err;
  } finally {
    clearTimeout(timeoutId);
    controller.signal.removeEventListener("abort", onParentAbort);
  }
}

// Same rendering function for both modes (UI-01): data -> jobState -> render().
function applySnapshot(data) {
  setJobState({
    job: {
      id: data.id,
      imageId: data.image_id,
      status: data.status,
      queuedAt: data.queued_at,
      startedAt: data.started_at,
      completedAt: data.completed_at,
      variants: data.variants || [],
      error: data.error || null,
      version: data.version,
      stage: data.stage,
      variantsCompleted: data.variants_completed ?? 0,
      simulationStep: data.simulation_step ?? 0,
    },
    retrievalError: false,
  });
}

function isTerminal(status) {
  return status === "completed" || status === "failed";
}

async function runObservation(jobId, mode, generation, controller) {
  // A loop is "current" only if nobody has stopped/replaced it since it started.
  const isCurrent = () => generation === observationGeneration && !controller.signal.aborted;
  const promptUrl = `/v1/jobs/${jobId}`;
  let lastVersion = 0;

  // Handles one received response. Returns true when observation must stop.
  const handle = ({ entry, result }, kind) => {
    if (!isCurrent()) {
      entry.outcome = "cancelled"; // late response from an old loop: ignored, not rendered
      return true;
    }
    if (result.status === 204) {
      // Normal holding-deadline expiry: NOT an error, NOT parsed, version unchanged.
      entry.outcome = "no_change_204";
      return false;
    }
    const data = result.data;
    entry.outcome =
      kind === "initial" ? "initial_200" : data.version > lastVersion ? "newer_200" : "unchanged_200";

    if (kind === "initial" && runLog.baselineVersion === null) runLog.baselineVersion = data.version; // first observed version = baseline
    lastVersion = Math.max(lastVersion, data.version); // `after` always equals the last version received

    applySnapshot(data);
    entry.render_ms = Date.now(); // render is synchronous (setJobState -> render)
    setJobState({ requestStats: computeStats(runLog.requests) });

    if (isTerminal(data.status)) {
      runLog.finalJob = data;
      setJobState({ polling: false }); // terminal: observation stops
      return true;
    }
    return false;
  };

  try {
    // UI-02: initial PROMPT snapshot (same request in both modes).
    const first = await observeOnce(promptUrl, POLL_TIMEOUT_MS, "initial", controller);
    if (handle(first, "initial")) return;

    while (isCurrent()) {
      let next;
      if (mode === "short") {
        await sleep(POLL_INTERVAL_MS, controller.signal); // delay AFTER the previous response
        if (!isCurrent()) return;
        next = await observeOnce(promptUrl, POLL_TIMEOUT_MS, "short", controller);
      } else {
        // Long polling: NO artificial pause; `after` = last version successfully received.
        const url = `${promptUrl}?after=${lastVersion}&wait=${LONG_POLL_WAIT_SECONDS}`;
        next = await observeOnce(url, LONG_POLL_TIMEOUT_MS, "long", controller);
      }
      if (handle(next, mode)) return;
      setJobState({ requestStats: computeStats(runLog.requests) });
    }
  } catch (err) {
    if (!isCurrent()) {
      setJobState({ requestStats: computeStats(runLog.requests) });
      return; // cancelled on purpose (new upload, mode switch, stop): not a retrieval error
    }
    // Retrieval error: keep the last known state, never infer that the worker failed.
    setJobState({ polling: false, retrievalError: true, requestStats: computeStats(runLog.requests) });
  }
}

function sleep(ms, signal) {
  return new Promise((resolve) => {
    const t = setTimeout(resolve, ms);
    signal.addEventListener(
      "abort",
      () => {
        clearTimeout(t);
        resolve();
      },
      { once: true }
    );
  });
}

// Counts every observation GET STARTED, split by outcome (assessment section 6).
// A request still pending has outcome null and is reported as "pending".
function computeStats(requests) {
  const count = (o) => requests.filter((r) => r.outcome === o).length;
  return {
    started: requests.length,
    initial_200: count("initial_200"),
    newer_200: count("newer_200"),
    unchanged_200: count("unchanged_200"),
    no_change_204: count("no_change_204"),
    errors: count("error"),
    cancelled: count("cancelled"),
    pending: requests.filter((r) => r.outcome === null).length,
  };
}

// Handles the "Try again" and "Resume" buttons: observe the SAME job again with
// the selected mode. It never calls POST /v1/images (no re-upload).
function handleTryAgain() {
  if (!jobState.job) return;
  beginObservation(jobState.job.id, { newRun: false });
}

// "Stop watching": cancels observation only. The worker keeps processing.
function handleStopObserving() {
  stopObserving();
  setJobState({
    polling: false,
    observationStopped: true,
    requestStats: computeStats(runLog.requests),
  });
}

// switching modes cancels the old loop and starts the new one
// on the SAME job. It never submits another image.
function handleModeChange(mode) {
  if (mode === state.observationMode) return;
  setState({ observationMode: mode });
  const job = jobState.job;
  const watching = job && !isTerminal(job.status) && !jobState.retrievalError && !jobState.observationStopped;
  if (watching) beginObservation(job.id, { newRun: true });
}

// Downloads the raw client log (JSON) for the experiment report.
function handleDownloadLog() {
  const payload = {
    job_id: runLog.jobId,
    mode: runLog.mode,
    image: runLog.image,
    baseline_version: runLog.baselineVersion,
    final_job: runLog.finalJob
      ? {
          status: runLog.finalJob.status,
          version: runLog.finalJob.version,
          started_at: runLog.finalJob.started_at,
          completed_at: runLog.finalJob.completed_at,
        }
      : null,
    requests: runLog.requests,
  };
  const blob = new Blob([JSON.stringify(payload, null, 2)], { type: "application/json" });
  const a = document.createElement("a");
  a.href = URL.createObjectURL(blob);
  a.download = `client_${runLog.mode || "mode"}_${(runLog.jobId || "job").slice(0, 8)}.json`;
  a.click();
  URL.revokeObjectURL(a.href);
}

const fileInput = document.getElementById("file-input");

fileInput.addEventListener("change", (e) => {
  handleFileSelected(e.target.files[0]);
  e.target.value = ""; // allow re-selecting the same file later
});

// "Choose image" / "Choose another image" are real buttons (keyboard-focusable) that open the file picker.
document.querySelectorAll("[data-choose]").forEach((btn) => {
  btn.addEventListener("click", () => fileInput.click());
});

// Drag & drop onto the drop zone. dragDepth counts enter/leave pairs so the
// highlight doesn't flicker as the cursor crosses child elements.
const dropzone = document.getElementById("dropzone");
let dragDepth = 0;

dropzone.addEventListener("dragenter", (e) => {
  e.preventDefault();
  dragDepth++;
  if (!state.isSubmitting) setState({ isDragging: true });
});
dropzone.addEventListener("dragover", (e) => {
  e.preventDefault(); // required, or the browser won't allow a drop
  e.dataTransfer.dropEffect = state.isSubmitting ? "none" : "copy";
});
dropzone.addEventListener("dragleave", () => {
  dragDepth = Math.max(0, dragDepth - 1);
  if (dragDepth === 0) setState({ isDragging: false });
});
dropzone.addEventListener("drop", (e) => {
  e.preventDefault();
  dragDepth = 0;
  setState({ isDragging: false });
  handleFileSelected(e.dataTransfer.files[0]);
});

// A file dropped outside the drop zone would make the browser navigate to it and lose the page.
["dragover", "drop"].forEach((type) => window.addEventListener(type, (e) => e.preventDefault()));

document.getElementById("process-button").addEventListener("click", handleProcessClick);
document.getElementById("try-again-button").addEventListener("click", handleTryAgain);
// new controls
document.getElementById("resume-button").addEventListener("click", handleTryAgain);
document.getElementById("stop-observing-button").addEventListener("click", handleStopObserving);
document.getElementById("download-log-button").addEventListener("click", handleDownloadLog);
document.querySelectorAll('input[name="observation-mode"]').forEach((radio) => {
  radio.addEventListener("change", (e) => handleModeChange(e.target.value));
});

// Ensure that polling is stopped when the user navigates away from the page to avoid unnecessary network requests and potential memory leaks.
window.addEventListener("beforeunload", stopObserving);

events.on("change", () => render(state, jobState));
events.on("jobChange", () => render(state, jobState));
render(state, jobState);