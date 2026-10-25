// Submits an image file to the server and returns the response.
async function submitImage(file) {
  const formData = new FormData();
  formData.append("image", file);

  const response = await fetch("/v1/images", {
    method: "POST",
    body: formData,
  });

  let body = null;
  try {
    body = await response.json();
  } catch {
    // A non-JSON body on failure still falls through to the status check below.
  }

  if (!response.ok) {
    const message =
      (body && typeof body.error === "string" && body.error) ||
      `Upload failed (HTTP ${response.status})`;
    throw new Error(message);
  }

  return body;
}

// Fetches the status of a job from the server and returns the response.
const JOB_STATUSES = ["queued", "processing", "completed", "failed"];

// Fetches the status of a job from the server and returns the response.
async function fetchObservation(url, signal) {
  const response = await fetch(url, { signal, cache: "no-store" });

  if (response.status === 204) {
    return { status: 204, data: null };
  }
  if (!response.ok) {
    throw new Error(`status check failed (HTTP ${response.status})`);
  }

  const data = await response.json(); // throws on an unparseable/empty body
  const valid =
    data &&
    typeof data.id === "string" &&
    JOB_STATUSES.includes(data.status) &&
    Number.isInteger(data.version) &&
    data.version >= 1;
  if (!valid) {
    throw new Error("invalid job representation");
  }
  return { status: response.status, data };
}
