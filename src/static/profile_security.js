(function() {
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

  function getJSON(url) {
    return fetch(url, {
      method: "GET",
      credentials: "same-origin"
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

  function escapeHTML(value) {
    return String(value || "")
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/\"/g, "&quot;")
      .replace(/'/g, "&#39;");
  }

  function joinPorts(values) {
    if (!Array.isArray(values) || !values.length) {
      return "";
    }

    return values.join(", ");
  }

  function joinLines(values) {
    if (!Array.isArray(values) || !values.length) {
      return "";
    }

    return values.join("\n");
  }

  function setCheckbox(form, name, checked) {
    var field = form.elements.namedItem(name);
    if (field) {
      field.checked = !!checked;
    }
  }

  function setValue(form, name, value) {
    var field = form.elements.namedItem(name);
    if (field) {
      field.value = value == null ? "" : String(value);
    }
  }

  function renderStatus(container, response, message) {
    if (!container) {
      return;
    }

    if (message) {
      container.innerHTML = '<div class="alert alert-green"><h5 class="alert-title">Candidate applied</h5><p>' + escapeHTML(message) + '</p></div>';
      return;
    }

    if (!response) {
      container.innerHTML = "";
      return;
    }

    var scenario = response.scenario || {};
    var targetCount = Number(response.target_candidate_count || 0);
    var uniqueCount = Number(response.unique_candidate_count || 0);
    var percent = 0;
    if (targetCount > 0) {
      percent = Math.round(Math.max(0, Math.min(100, (uniqueCount / targetCount) * 100)));
    } else if (response.done) {
      percent = 100;
    }
    var parts = [];
    if (response.progress_message) {
      parts.push(response.progress_message);
    }
    parts.push(String(uniqueCount || 0) + " unique candidates evaluated");
    if (response.duplicate_candidate_count) {
      parts.push(String(response.duplicate_candidate_count) + " duplicates skipped");
    }
    if (response.current_round && response.total_rounds) {
      parts.push("round " + response.current_round + " of " + response.total_rounds);
    }
    if (Array.isArray(scenario.required_tcp_ports) && scenario.required_tcp_ports.length) {
      parts.push("required TCP: " + scenario.required_tcp_ports.join(", "));
    }
    if (Array.isArray(scenario.required_udp_ports) && scenario.required_udp_ports.length) {
      parts.push("required UDP: " + scenario.required_udp_ports.join(", "));
    }

    var alertClass = response.done ? (response.status === "failed" ? "alert-red" : "alert-grey") : "alert-grey";
    var title = "Search Complete";
    if (!response.done) {
      title = "Searching";
    } else if (response.status === "failed") {
      title = "Search Failed";
    }
    if (response.error) {
      parts.push(response.error);
    }

    container.innerHTML = '' +
      '<div class="alert ' + alertClass + '">' +
        '<div class="profile-security-search-status-head">' +
          '<div>' +
            '<h5 class="alert-title">' + escapeHTML(title) + '</h5>' +
            '<p>' + escapeHTML(parts.join(" - ")) + '</p>' +
          '</div>' +
          '<div class="profile-security-search-status-metric">' + escapeHTML(String(percent)) + '%</div>' +
        '</div>' +
        '<div class="profile-security-search-progress-shell" aria-hidden="true">' +
          '<div class="profile-security-search-progress-bar" style="width: ' + escapeHTML(String(percent)) + '%"></div>' +
        '</div>' +
      '</div>';
  }

  function renderResults(container, response, form, statusContainer) {
    if (!container) {
      return;
    }

    var candidates = response && Array.isArray(response.candidates) ? response.candidates : [];
    if (!candidates.length) {
      container.innerHTML = '<p class="muted-text">No candidates returned yet.</p>';
      return;
    }

    container.innerHTML = '<div class="profile-security-search-results-grid">' + candidates.map(function(candidate) {
      var evaluation = candidate.evaluation || {};
      var errors = Array.isArray(evaluation.errors) ? evaluation.errors : [];
      var warnings = Array.isArray(evaluation.warnings) ? evaluation.warnings : [];
      var validLabel = evaluation.valid ? "Ready" : "Needs review";
      var validationClass = evaluation.valid ? "alert-green" : "alert-yellow";
      var tcpPorts = candidate.config && candidate.config.firewall ? joinPorts(candidate.config.firewall.allowed_tcp_ports) : "";
      var udpPorts = candidate.config && candidate.config.firewall ? joinPorts(candidate.config.firewall.allowed_udp_ports) : "";
      var modules = joinLines(candidate.config ? candidate.config.blacklisted_kernel_modules : []);
      var blockedCategories = candidate.config && candidate.config.website_blocking ? joinPorts(candidate.config.website_blocking.block_categories) : "";
      var pwquality = candidate.config && candidate.config.password_policy ? candidate.config.password_policy.pwquality : null;
      var expiry = candidate.config && candidate.config.password_policy ? candidate.config.password_policy.expiry : null;

      return '' +
        '<article class="profile-security-search-candidate-card" data-profile-security-candidate-index="' + escapeHTML(String(candidate.rank || 0)) + '">' +
          '<div class="profile-security-search-candidate-head">' +
            '<div>' +
              '<strong>Candidate #' + escapeHTML(String(candidate.rank || 0)) + '</strong>' +
              '<p class="muted-text">' + escapeHTML(candidate.summary || '') + '</p>' +
            '</div>' +
            '<span class="profile-security-search-chip">Score ' + escapeHTML(String(candidate.score || 0)) + '</span>' +
          '</div>' +
          '<div class="alert ' + validationClass + '"><h5 class="alert-title">' + escapeHTML(validLabel) + '</h5><p>' + escapeHTML((errors.length ? errors.join('; ') : 'Candidate passed the fixed evaluator checks so far.')) + '</p></div>' +
          '<p><strong>Rationale:</strong> ' + escapeHTML(candidate.rationale || "") + '</p>' +
          '<div class="profile-security-search-fact-grid">' +
            '<div class="profile-security-search-fact"><span class="profile-security-search-fact-label">Firewall</span><span class="profile-security-search-fact-value">' + ((candidate.config && candidate.config.firewall && candidate.config.firewall.enable) ? 'Enabled' : 'Disabled') + '</span></div>' +
            '<div class="profile-security-search-fact"><span class="profile-security-search-fact-label">TCP</span><span class="profile-security-search-fact-value">' + escapeHTML(tcpPorts || 'none') + '</span></div>' +
            '<div class="profile-security-search-fact"><span class="profile-security-search-fact-label">UDP</span><span class="profile-security-search-fact-value">' + escapeHTML(udpPorts || 'none') + '</span></div>' +
            '<div class="profile-security-search-fact"><span class="profile-security-search-fact-label">AppArmor</span><span class="profile-security-search-fact-value">' + ((candidate.config && candidate.config.apparmor && candidate.config.apparmor.enable) ? 'Enabled' : 'Disabled') + '</span></div>' +
            '<div class="profile-security-search-fact"><span class="profile-security-search-fact-label">PWQuality</span><span class="profile-security-search-fact-value">' + ((pwquality && pwquality.enable) ? ('Min ' + pwquality.minimum_length) : 'Disabled') + '</span></div>' +
            '<div class="profile-security-search-fact"><span class="profile-security-search-fact-label">Expiry</span><span class="profile-security-search-fact-value">' + ((expiry && expiry.enable) ? (expiry.maximum_days + ' days') : 'Disabled') + '</span></div>' +
          '</div>' +
          '<p><strong>Kernel modules:</strong> ' + escapeHTML(modules || 'none') + '</p>' +
          '<p><strong>Website blocking:</strong> ' + ((candidate.config && candidate.config.website_blocking && candidate.config.website_blocking.enable) ? ('Enabled (' + escapeHTML(blockedCategories || 'default lists') + ')') : 'Disabled') + '</p>' +
          (warnings.length ? '<p><strong>Warnings:</strong> ' + escapeHTML(warnings.join('; ')) + '</p>' : '') +
          '<div class="form-actions"><button type="button" class="btn" data-profile-security-apply-candidate>Apply Candidate To Form</button></div>' +
        '</article>';
    }).join("") + '</div>';

    Array.prototype.forEach.call(container.querySelectorAll("[data-profile-security-apply-candidate]"), function(button, index) {
      button.addEventListener("click", function() {
        var candidate = candidates[index];
        if (!candidate || !candidate.config || !form) {
          return;
        }

        var config = candidate.config;
        var pwquality = config.password_policy ? config.password_policy.pwquality : null;
        var expiry = config.password_policy ? config.password_policy.expiry : null;

        setCheckbox(form, "firewall_enabled", config.firewall && config.firewall.enable);
        setValue(form, "firewall_allowed_tcp_ports", joinPorts(config.firewall ? config.firewall.allowed_tcp_ports : []));
        setValue(form, "firewall_allowed_udp_ports", joinPorts(config.firewall ? config.firewall.allowed_udp_ports : []));
        setValue(form, "blacklisted_kernel_modules", joinLines(config.blacklisted_kernel_modules || []));
        setCheckbox(form, "apparmor_enabled", config.apparmor && config.apparmor.enable);

        setCheckbox(form, "password_pwquality_enabled", pwquality && pwquality.enable);
        setValue(form, "password_pwquality_min_length", pwquality && pwquality.enable ? pwquality.minimum_length : "");
        setValue(form, "password_pwquality_min_digits", pwquality && pwquality.enable ? pwquality.minimum_digits : "");
        setValue(form, "password_pwquality_min_upper", pwquality && pwquality.enable ? pwquality.minimum_upper : "");
        setValue(form, "password_pwquality_min_lower", pwquality && pwquality.enable ? pwquality.minimum_lower : "");
        setValue(form, "password_pwquality_min_other", pwquality && pwquality.enable ? pwquality.minimum_other : "");
        setValue(form, "password_pwquality_retry_count", pwquality && pwquality.enable ? pwquality.retry_count : "");

        setCheckbox(form, "password_expiry_enabled", expiry && expiry.enable);
        setValue(form, "password_expiry_max_days", expiry && expiry.enable ? expiry.maximum_days : "");
        setValue(form, "password_expiry_warning_days", expiry && expiry.enable ? expiry.warning_days : "");

        setCheckbox(form, "website_blocking_enabled", config.website_blocking && config.website_blocking.enable);
        ["fakenews", "gambling", "porn", "social"].forEach(function(category) {
          var selected = !!(config.website_blocking && Array.isArray(config.website_blocking.block_categories) && config.website_blocking.block_categories.indexOf(category) >= 0);
          setCheckbox(form, "website_block_" + category, selected);
        });

        renderStatus(statusContainer, null, "Candidate #" + (candidate.rank || index + 1) + " is loaded into the form below. Review it, then click Save Security Settings to persist it.");
        form.scrollIntoView({ behavior: "smooth", block: "start" });
      });
    });
  }

  function init() {
    var root = document.querySelector("[data-profile-security-search-root]");
    if (!root) {
      return;
    }

    var goalField = root.querySelector("[data-profile-security-search-goal]");
    var button = root.querySelector("[data-profile-security-search-button]");
    var statusContainer = root.querySelector("[data-profile-security-search-status]");
    var resultsContainer = root.querySelector("[data-profile-security-search-results]");
    var form = document.querySelector("[data-profile-security-form]");
    var searchURL = root.getAttribute("data-profile-security-search-url") || "";
    var pollTimer = null;
    var activeStatusPath = "";

    if (!goalField || !button || !searchURL) {
      return;
    }

    function stopPolling() {
      if (pollTimer) {
        window.clearTimeout(pollTimer);
        pollTimer = null;
      }
    }

    function setSearching(searching) {
      button.disabled = !!searching;
      goalField.disabled = !!searching;
      button.textContent = searching ? "Searching..." : "Generate Security Candidates";
    }

    function pollStatus() {
      if (!activeStatusPath) {
        stopPolling();
        setSearching(false);
        return;
      }

      getJSON(activeStatusPath).then(function(response) {
        renderStatus(statusContainer, response, "");
        renderResults(resultsContainer, response, form, statusContainer);

        if (response.done) {
          stopPolling();
          setSearching(false);
          return;
        }

        pollTimer = window.setTimeout(pollStatus, 1200);
      }).catch(function(err) {
        stopPolling();
        setSearching(false);
        if (statusContainer) {
          statusContainer.innerHTML = '<div class="alert alert-red"><h5 class="alert-title">Search Failed</h5><p>' + escapeHTML(err.message || "Security search failed") + '</p></div>';
        }
      });
    }

    button.addEventListener("click", function() {
      var goal = (goalField.value || "").trim();
      if (!goal) {
        window.alert("Enter the operating system goal first.");
        goalField.focus();
        return;
      }

      stopPolling();
      activeStatusPath = "";
      setSearching(true);
      if (statusContainer) {
        statusContainer.innerHTML = '<div class="alert alert-grey"><h5 class="alert-title">Queued</h5><p>Starting background security search...</p></div>';
      }
      if (resultsContainer) {
        resultsContainer.innerHTML = "";
      }

      postJSON(searchURL, { goal: goal }).then(function(response) {
        activeStatusPath = response && response.status_path ? response.status_path : "";
        if (!activeStatusPath) {
          throw new Error("Missing security search status path");
        }

        pollStatus();
      }).catch(function(err) {
        stopPolling();
        setSearching(false);
        if (statusContainer) {
          statusContainer.innerHTML = '<div class="alert alert-red"><h5 class="alert-title">Search Failed</h5><p>' + escapeHTML(err.message || "Security search failed") + '</p></div>';
        }
      });
    });
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", init);
    return;
  }

  init();
})();
