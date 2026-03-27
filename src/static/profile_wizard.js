(function() {
  function parseState() {
    var node = document.getElementById("profile-wizard-state");
    if (!node) {
      return null;
    }

    try {
      return JSON.parse(node.value || node.textContent || "{}");
    } catch (err) {
      return null;
    }
  }

  function getCSRFToken() {
    var meta = document.querySelector('meta[name="csrf-token"]');
    if (!meta) {
      return "";
    }

    return meta.getAttribute("content") || "";
  }

  function postJSON(url, body) {
    var headers = {
      "Content-Type": "application/json"
    };
    var csrfToken = getCSRFToken();
    if (csrfToken) {
      headers["X-CSRF-Token"] = csrfToken;
    }

    return fetch(url, {
      method: "POST",
      credentials: "same-origin",
      headers: headers,
      body: JSON.stringify(body || {})
    }).then(function(response) {
      return response.json().catch(function() {
        return {};
      }).then(function(data) {
        if (!response.ok) {
          var message = data && data.error ? data.error : "Request failed";
          throw new Error(message);
        }

        return data;
      });
    });
  }

  function streamChat(url, body, handlers) {
    var headers = {
      "Content-Type": "application/json"
    };
    var csrfToken = getCSRFToken();
    if (csrfToken) {
      headers["X-CSRF-Token"] = csrfToken;
    }

    return fetch(url, {
      method: "POST",
      credentials: "same-origin",
      headers: headers,
      body: JSON.stringify(body || {})
    }).then(function(response) {
      if (!response.ok || !response.body) {
        throw new Error("Request failed");
      }

      var reader = response.body.getReader();
      var decoder = new TextDecoder();
      var buffer = "";
      var currentEvent = "";

      function consume() {
        return reader.read().then(function(result) {
          if (result.done) {
            return;
          }

          buffer += decoder.decode(result.value, { stream: true });
          var lines = buffer.split("\n");
          buffer = lines.pop();

          lines.forEach(function(line) {
            if (line.indexOf("event: ") === 0) {
              currentEvent = line.substring(7);
              return;
            }

            if (line.indexOf("data: ") !== 0) {
              return;
            }

            var data = line.substring(6);
            if (currentEvent === "chunk" && handlers.onChunk) {
              handlers.onChunk(data);
            } else if (currentEvent === "state" && handlers.onState) {
              try {
                handlers.onState(JSON.parse(data));
              } catch (err) {
                throw err;
              }
            } else if (currentEvent === "error" && handlers.onError) {
              handlers.onError(data);
            } else if (currentEvent === "done" && handlers.onDone) {
              handlers.onDone();
            }
          });

          return consume();
        });
      }

      return consume();
    });
  }

  function escapeHTML(value) {
    return String(value || "")
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/\"/g, "&quot;")
      .replace(/'/g, "&#39;");
  }

  function renderMarkdown(value) {
    var text = String(value || "");
    var marked = window.marked || window.profileWizardMarked;
    if (!marked || typeof marked.parse !== "function") {
      return escapeHTML(text);
    }

    try {
      return marked.parse(text);
    } catch (err) {
      return escapeHTML(text);
    }
  }

  function renderConversation(state, root) {
    var container = root.querySelector("[data-profile-wizard-chat]");
    if (!container) {
      return;
    }

    var messages = state && Array.isArray(state.conversation) ? state.conversation : [];
    if (!messages.length) {
      container.innerHTML = '<p class="muted-text">Start chatting to build the draft.</p>';
      return;
    }

    container.innerHTML = messages.map(function(message) {
      var role = message.role === "user" ? "user" : "assistant";
      var body = role === "assistant" ? renderMarkdown(message.content) : escapeHTML(message.content);
      return '' +
        '<article class="profile-wizard-message profile-wizard-message-' + role + '">' +
          '<div class="profile-wizard-message-role">' + (role === "user" ? "You" : "AI Wizard") + '</div>' +
          '<div class="profile-wizard-message-body">' + body + '</div>' +
        '</article>';
    }).join("");

    container.scrollTop = container.scrollHeight;
  }

  function renderValidation(state, root) {
    var container = root.querySelector("[data-profile-wizard-validation]");
    if (!container) {
      return;
    }

    var validation = state && state.validation ? state.validation : null;
    var html = "";
    if (!validation) {
      container.innerHTML = html;
      return;
    }

    if (validation.errors && validation.errors.length) {
      html += '<div class="alert alert-red"><h5 class="alert-title">Apply blocked</h5><p>' + escapeHTML(validation.errors.join("; ")) + '</p></div>';
    } else if (validation.can_apply) {
      html += '<div class="alert alert-green"><h5 class="alert-title">Ready</h5><p>The draft is valid and can be applied.</p></div>';
    }

    if (validation.warnings && validation.warnings.length) {
      html += '<div class="alert alert-grey"><h5 class="alert-title">Heads up</h5><p>' + escapeHTML(validation.warnings.join("; ")) + '</p></div>';
    }

    container.innerHTML = html;
  }

  function renderDraft(state, root) {
    var container = root.querySelector("[data-profile-wizard-draft]");
    if (!container) {
      return;
    }

    var draft = state && state.draft ? state.draft : null;
    if (!draft) {
      container.innerHTML = '<p class="muted-text">No draft yet.</p>';
      return;
    }

    var fleets = draft.fleet_names && draft.fleet_names.length ? draft.fleet_names : draft.fleet_ids || [];
    var packages = draft.packages || [];
    var plannedChanges = draft.planned_changes || [];
    var rawNix = draft.has_raw_nix ? '<pre class="profile-wizard-code-block">' + escapeHTML(draft.raw_nix) + '</pre>' : '<p class="muted-text">No raw Nix override.</p>';
    var plannedChangesHTML = plannedChanges.length ? '<div class="profile-wizard-plan-list">' + plannedChanges.map(function(change) {
      return '' +
        '<article class="profile-wizard-plan-item">' +
          '<div class="profile-wizard-plan-label">' + escapeHTML(change.label) + '</div>' +
          '<div class="profile-wizard-plan-detail">' + escapeHTML(change.detail) + '</div>' +
        '</article>';
    }).join("") + '</div>' : '<p class="muted-text">No planned changes yet. Keep chatting to shape the draft.</p>';

    container.innerHTML = '' +
      '<section class="profile-wizard-summary-block">' +
        '<h4>AI Plans To Change</h4>' +
        '<p class="muted-text">' + escapeHTML(String(draft.planned_change_count || 0)) + ' planned change' + ((draft.planned_change_count || 0) === 1 ? '' : 's') + '</p>' +
        plannedChangesHTML +
      '</section>' +
      '<section class="profile-wizard-summary-block">' +
        '<h4>Metadata</h4>' +
        '<p><strong>Name:</strong> ' + (draft.name ? escapeHTML(draft.name) : '<span class="muted-text">Not set</span>') + '</p>' +
        '<p><strong>Description:</strong> ' + (draft.description ? escapeHTML(draft.description) : '<span class="muted-text">None</span>') + '</p>' +
        '<p><strong>Fleets:</strong> ' + (fleets.length ? escapeHTML(fleets.join(", ")) : '<span class="muted-text">None selected</span>') + '</p>' +
      '</section>' +
      '<section class="profile-wizard-summary-block">' +
        '<h4>Packages</h4>' +
        '<p><strong>Count:</strong> ' + escapeHTML(String(draft.package_count || 0)) + '</p>' +
        (packages.length ? '<div class="profile-wizard-token-list">' + packages.map(function(pkg) {
          return '<code>' + escapeHTML(pkg) + '</code>';
        }).join("") + '</div>' : '<p class="muted-text">No packages selected.</p>') +
      '</section>' +
      '<section class="profile-wizard-summary-block">' +
        '<h4>Kernel</h4>' +
        '<p>' + escapeHTML(draft.kernel && draft.kernel.summary ? draft.kernel.summary : 'Default from pinned nixpkgs') + '</p>' +
      '</section>' +
      '<section class="profile-wizard-summary-block">' +
        '<h4>OpenClaw</h4>' +
        '<p>' + escapeHTML(draft.openclaw_summary || 'Disabled') + '</p>' +
      '</section>' +
      '<section class="profile-wizard-summary-block">' +
        '<h4>Raw Nix</h4>' + rawNix +
      '</section>';
  }

  function syncApplyButton(state, root) {
    var button = root.querySelector("[data-profile-wizard-apply]");
    if (!button) {
      return;
    }

    var canApply = !!(state && state.available && state.validation && state.validation.can_apply);
    button.disabled = !canApply;
    button.textContent = state && state.mode === "adapt" ? "Apply Changes" : "Create Profile";
  }

  function render(state, root) {
    renderConversation(state, root);
    renderValidation(state, root);
    renderDraft(state, root);
    syncApplyButton(state, root);
  }

  function init() {
    var root = document.querySelector("[data-profile-wizard-root]");
    if (!root) {
      return;
    }

    var state = parseState();
    if (!state) {
      return;
    }

    render(state, root);

    var form = root.querySelector("[data-profile-wizard-form]");
    var textarea = document.getElementById("profile-wizard-message");
    var sendButton = root.querySelector("[data-profile-wizard-send]");
    var discardButton = root.querySelector("[data-profile-wizard-discard]");
    var applyButton = root.querySelector("[data-profile-wizard-apply]");

    if (form && textarea && sendButton) {
      textarea.addEventListener("keydown", function(event) {
        if (event.key !== "Enter" || event.shiftKey) {
          return;
        }

        event.preventDefault();
        if (sendButton.disabled) {
          return;
        }

        form.requestSubmit();
      });

      form.addEventListener("submit", function(event) {
        event.preventDefault();
        var message = (textarea.value || "").trim();
        if (!message || !state.chat_path) {
          return;
        }

        var conversation = Array.isArray(state.conversation) ? state.conversation.slice() : [];
        conversation.push({ role: "user", content: message });
        conversation.push({ role: "assistant", content: "" });
        render(Object.assign({}, state, { conversation: conversation }), root);

        textarea.disabled = true;
        sendButton.disabled = true;
        sendButton.textContent = "Sending...";

        var streamedReply = "";
        streamChat(state.chat_path, { message: message }, {
          onChunk: function(chunk) {
            streamedReply += chunk;
            conversation[conversation.length - 1].content = streamedReply;
            render(Object.assign({}, state, { conversation: conversation }), root);
          },
          onState: function(nextState) {
            state = nextState;
          },
          onError: function(messageText) {
            if (!streamedReply) {
              conversation[conversation.length - 1].content = "Error: " + messageText;
              render(Object.assign({}, state, { conversation: conversation }), root);
            }
          },
          onDone: function() {
            if (state) {
              textarea.value = "";
              render(state, root);
              textarea.focus();
            }
          }
        }).catch(function(err) {
          window.alert(err.message || "Request failed");
        }).finally(function() {
          textarea.disabled = !state.available;
          sendButton.disabled = !state.available;
          sendButton.textContent = "Send";
        });
      });
    }

    if (discardButton) {
      discardButton.addEventListener("click", function() {
        if (!state.discard_path) {
          return;
        }

        discardButton.disabled = true;
        postJSON(state.discard_path, {}).then(function(response) {
          if (response && response.redirect) {
            window.location.assign(response.redirect);
            return;
          }

          window.location.assign(state.manual_path || "/profiles");
        }).catch(function(err) {
          window.alert(err.message || "Discard failed");
        }).finally(function() {
          discardButton.disabled = false;
        });
      });
    }

    if (applyButton) {
      applyButton.addEventListener("click", function() {
        if (!state.apply_path || !state.validation || !state.validation.can_apply) {
          return;
        }

        applyButton.disabled = true;
        applyButton.textContent = "Applying...";

        postJSON(state.apply_path, {}).then(function(response) {
          if (response && response.redirect) {
            window.location.assign(response.redirect);
            return;
          }
          window.location.reload();
        }).catch(function(err) {
          window.alert(err.message || "Apply failed");
          render(state, root);
        }).finally(function() {
          applyButton.textContent = state.mode === "adapt" ? "Apply Changes" : "Create Profile";
          syncApplyButton(state, root);
        });
      });
    }
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", init);
    return;
  }

  init();
})();
