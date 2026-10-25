// A simple event emitter implementation that allows registering, unregistering, 
// and emitting events with payloads. This is used for managing state changes and notifying listeners in the UI.
class EventEmitter {
  constructor() {
    this.listeners = {};
  }

  on(event, callback) {
    (this.listeners[event] ??= []).push(callback);
    return () => this.off(event, callback);
  }

  off(event, callback) {
    if (!this.listeners[event]) return;
    this.listeners[event] = this.listeners[event].filter((cb) => cb !== callback);
  }

  emit(event, payload) {
    (this.listeners[event] || []).forEach((cb) => cb(payload));
  }
}
