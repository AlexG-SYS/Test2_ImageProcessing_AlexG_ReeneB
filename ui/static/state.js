// Single source of truth for the upload flow's UI state. Nothing else in
// the app holds its own copy of these values
const UploadPhase = {
  IDLE: "idle", // no file, no job, no results
  SELECTED: "selected", // local preview only, nothing sent yet
  UPLOADING: "uploading", // POST in flight
  ACCEPTED: "accepted", // Terminal state: stored, no job/polling yet
  ERROR: "error", // rejected upload or failed request
};

const events = new EventEmitter();

const state = {
  phase: UploadPhase.IDLE,
  selectedFile: null,
  previewUrl: null,
  previewDimensions: null, // { width, height } once the browser has decoded the preview
  isDragging: false, // a file is currently being dragged over the drop zone
  isSubmitting: false, // guards against overlapping POSTs from this page
  errorMessage: null,
  acceptedImage: null, // { image_id, original_filename, media_type, size_bytes }
  // the observation mode the user selected ("short" | "long").
  // It is a user preference, so it is NOT cleared by resetToIdle / resetJobState.
  observationMode: "short",
};

function setState(patch) {
  Object.assign(state, patch);
  events.emit("change", state);
}

function resetToIdle() {
  if (state.previewUrl) URL.revokeObjectURL(state.previewUrl);
  setState({
    phase: UploadPhase.IDLE,
    selectedFile: null,
    previewUrl: null,
    previewDimensions: null,
    isDragging: false,
    errorMessage: null,
    acceptedImage: null,
  });
}

// Single source of truth for the job polling state. Nothing else in the app
// holds its own copy of these values. The job object is null until a job is
// created, and then it is updated with the latest status on each poll.
const jobState = {
  job: null, // { id, imageId, status, queuedAt, startedAt, completedAt, variants, error } once a job exists
  source: null, // { name, mediaType } of the file that was submitted; used to name downloads
  polling: false, // true only while the 1-second loop is actively running
  retrievalError: false, // true after a failed GET, until Try again or a new job starts
  // mode of the loop that is (or was last) observing this job; shown in the job card.
  mode: "short",
  // true after the user pressed "Stop watching": observation is cancelled but the worker keeps going.
  observationStopped: false,
  // request counters for the current observation run (see computeStats in app.js).
  requestStats: null,
};

function setJobState(patch) {
  Object.assign(jobState, patch);
  events.emit("jobChange", jobState);
}

function resetJobState() {
  jobState.job = null;
  jobState.source = null;
  jobState.polling = false;
  jobState.retrievalError = false;
  jobState.observationStopped = false; // [V2]
  jobState.requestStats = null; // [V2]
  events.emit("jobChange", jobState);
}