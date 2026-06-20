/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
(function() {
  var form = document.getElementById("foreign-imports-form");
  if (!form) {
    return;
  }

  var profileID = form.getAttribute("data-profile-id") || "";
  var container = document.getElementById("foreign-flakes");
  var template = document.getElementById("foreign-flake-template");
  var addButton = document.getElementById("add-foreign-flake");

  function getCSRFToken() {
    var meta = document.querySelector('meta[name="csrf-token"]');
    return meta ? (meta.getAttribute("content") || "") : "";
  }

  function postForm(url, params) {
    var body = new URLSearchParams();
    Object.keys(params).forEach(function(key) {
      body.append(key, params[key]);
    });

    var headers = { "Content-Type": "application/x-www-form-urlencoded" };
    var token = getCSRFToken();
    if (token) {
      headers["X-CSRF-Token"] = token;
    }

    return fetch(url, {
      method: "POST",
      credentials: "same-origin",
      headers: headers,
      body: body.toString()
    }).then(function(response) {
      return response.json().catch(function() {
        return {};
      }).then(function(data) {
        if (!response.ok) {
          throw new Error((data && data.error) || "Request failed");
        }
        return data;
      });
    });
  }

  function nextIndex() {
    var max = -1;
    container.querySelectorAll(".foreign-flake").forEach(function(row) {
      var idx = parseInt(row.getAttribute("data-index"), 10);
      if (!isNaN(idx) && idx > max) {
        max = idx;
      }
    });
    return max + 1;
  }

  function selectedModules(row) {
    var values = [];
    row.querySelectorAll(".flake-modules-list input[type=checkbox]:checked").forEach(function(box) {
      values.push(box.value);
    });
    return values;
  }

  function renderModules(row, modules, checkedValues) {
    var index = row.getAttribute("data-index");
    var list = row.querySelector(".flake-modules-list");
    var hint = row.querySelector(".flake-modules-hint");
    list.innerHTML = "";

    var checkedSet = {};
    (checkedValues || []).forEach(function(value) {
      checkedSet[value] = true;
    });

    if (!modules.length) {
      if (hint) {
        hint.textContent = "This flake does not expose any nixosModules.";
      }
      return;
    }

    if (hint) {
      hint.textContent = "Select the modules to import into this profile.";
    }

    modules.forEach(function(name) {
      var label = document.createElement("label");
      label.className = "checkbox-row";

      var input = document.createElement("input");
      input.type = "checkbox";
      input.name = "flake_modules_" + index;
      input.value = name;
      if (checkedSet[name]) {
        input.checked = true;
      }

      var code = document.createElement("code");
      code.textContent = name;

      label.appendChild(input);
      label.appendChild(document.createTextNode(" "));
      label.appendChild(code);
      list.appendChild(label);
    });
  }

  function loadModules(row, button) {
    var refInput = row.querySelector(".flake-ref");
    var tokenInput = row.querySelector(".flake-token");
    var revInput = row.querySelector(".flake-rev");
    var status = row.querySelector(".flake-status");
    var pinned = row.querySelector(".flake-pinned");
    var pinnedRev = row.querySelector(".flake-pinned-rev");

    var ref = (refInput.value || "").trim();
    if (!ref) {
      status.textContent = "Enter a flake reference first.";
      return;
    }

    var previouslyChecked = selectedModules(row);
    var originalLabel = button.textContent;
    button.disabled = true;
    button.textContent = "Loading…";
    status.textContent = "Resolving flake and fetching modules…";

    postForm("/profiles/" + profileID + "/foreign-imports/modules", {
      flake_ref: ref,
      flake_token: (tokenInput.value || "")
    }).then(function(data) {
      if (data.error) {
        status.textContent = data.error;
        button.textContent = originalLabel;
        return;
      }

      if (data.rev) {
        revInput.value = data.rev;
        if (pinnedRev) {
          pinnedRev.textContent = data.rev;
        }
        if (pinned) {
          pinned.hidden = false;
        }
      }

      renderModules(row, data.modules || [], previouslyChecked);
      status.textContent = "Loaded " + (data.modules ? data.modules.length : 0) + " module(s).";
      button.textContent = "Update";
    }).catch(function(err) {
      status.textContent = err.message || "Failed to load modules.";
      button.textContent = originalLabel;
    }).then(function() {
      button.disabled = false;
    });
  }

  function wireRow(row) {
    var loadButton = row.querySelector(".flake-load");
    if (loadButton) {
      loadButton.addEventListener("click", function() {
        loadModules(row, loadButton);
      });
    }

    var removeButton = row.querySelector(".flake-remove");
    if (removeButton) {
      removeButton.addEventListener("click", function() {
        row.parentNode.removeChild(row);
      });
    }
  }

  container.querySelectorAll(".foreign-flake").forEach(wireRow);

  if (addButton) {
    addButton.addEventListener("click", function() {
      var index = nextIndex();
      var html = template.innerHTML.replace(/__INDEX__/g, String(index));
      var wrapper = document.createElement("div");
      wrapper.innerHTML = html.trim();
      var row = wrapper.firstChild;
      container.appendChild(row);
      wireRow(row);
    });
  }
})();
