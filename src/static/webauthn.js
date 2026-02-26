(function() {
  function bufferEncode(value) {
    var bytes = new Uint8Array(value);
    var binary = "";
    for (var i = 0; i < bytes.byteLength; i++) {
      binary += String.fromCharCode(bytes[i]);
    }
    return btoa(binary).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
  }

  function bufferDecode(value) {
    var base64 = value.replace(/-/g, "+").replace(/_/g, "/");
    var padLength = base64.length % 4;
    if (padLength) {
      base64 += "=".repeat(4 - padLength);
    }
    var binary = atob(base64);
    var bytes = new Uint8Array(binary.length);
    for (var i = 0; i < binary.length; i++) {
      bytes[i] = binary.charCodeAt(i);
    }
    return bytes.buffer;
  }

  function toArrayBuffer(value) {
    if (value instanceof ArrayBuffer) {
      return value;
    }
    if (ArrayBuffer.isView(value)) {
      return value.buffer.slice(value.byteOffset, value.byteOffset + value.byteLength);
    }
    return value;
  }

  function isPublicKeyCredential(value) {
    if (!value || typeof value !== "object") {
      return false;
    }
    if (window.PublicKeyCredential && value instanceof PublicKeyCredential) {
      return true;
    }
    return "rawId" in value && "response" in value && "type" in value;
  }

  function serializePublicKeyCredential(credential) {
    if (!credential) {
      return credential;
    }

    var rawId = credential.rawId ? bufferEncode(toArrayBuffer(credential.rawId)) : "";
    var id = credential.id || rawId;

    var result = {
      id: id,
      rawId: rawId,
      type: credential.type,
    };

    if (credential.authenticatorAttachment) {
      result.authenticatorAttachment = credential.authenticatorAttachment;
    }

    var clientExtensionResults = null;
    if (typeof credential.getClientExtensionResults === "function") {
      clientExtensionResults = credential.getClientExtensionResults();
    } else if (credential.clientExtensionResults) {
      clientExtensionResults = credential.clientExtensionResults;
    }
    if (clientExtensionResults) {
      result.clientExtensionResults = serializeCredential(clientExtensionResults);
    }

    var response = credential.response || {};
    var responseData = {};
    if (response.clientDataJSON) {
      responseData.clientDataJSON = bufferEncode(toArrayBuffer(response.clientDataJSON));
    }
    if (response.attestationObject) {
      responseData.attestationObject = bufferEncode(toArrayBuffer(response.attestationObject));
    }
    if (response.authenticatorData) {
      responseData.authenticatorData = bufferEncode(toArrayBuffer(response.authenticatorData));
    }
    if (response.signature) {
      responseData.signature = bufferEncode(toArrayBuffer(response.signature));
    }
    if (response.userHandle) {
      responseData.userHandle = bufferEncode(toArrayBuffer(response.userHandle));
    }
    if (typeof response.getTransports === "function") {
      var transports = response.getTransports();
      if (transports && transports.length) {
        responseData.transports = transports;
      }
    } else if (response.transports) {
      responseData.transports = response.transports;
    }
    result.response = responseData;

    return result;
  }

  function decodeCreationOptions(options) {
    if (!options || !options.publicKey) {
      return options;
    }
    var publicKey = options.publicKey;
    if (typeof publicKey.challenge === "string") {
      publicKey.challenge = bufferDecode(publicKey.challenge);
    }
    if (publicKey.user && typeof publicKey.user.id === "string") {
      publicKey.user.id = bufferDecode(publicKey.user.id);
    }
    if (publicKey.excludeCredentials) {
      publicKey.excludeCredentials = publicKey.excludeCredentials.map(function(cred) {
        if (typeof cred.id === "string") {
          cred.id = bufferDecode(cred.id);
        }
        return cred;
      });
    }
    return options;
  }

  function decodeRequestOptions(options) {
    if (!options || !options.publicKey) {
      return options;
    }
    var publicKey = options.publicKey;
    if (typeof publicKey.challenge === "string") {
      publicKey.challenge = bufferDecode(publicKey.challenge);
    }
    if (publicKey.allowCredentials) {
      publicKey.allowCredentials = publicKey.allowCredentials.map(function(cred) {
        if (typeof cred.id === "string") {
          cred.id = bufferDecode(cred.id);
        }
        return cred;
      });
    }
    return options;
  }

  function serializeCredential(value) {
    if (isPublicKeyCredential(value)) {
      return serializePublicKeyCredential(value);
    }
    if (value instanceof ArrayBuffer || ArrayBuffer.isView(value)) {
      return bufferEncode(toArrayBuffer(value));
    }
    if (Array.isArray(value)) {
      return value.map(serializeCredential);
    }
    if (value && typeof value === "object") {
      var obj = {};
      Object.keys(value).forEach(function(key) {
        obj[key] = serializeCredential(value[key]);
      });
      return obj;
    }
    return value;
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
      "Content-Type": "application/json",
    };
    var csrfToken = getCSRFToken();
    if (csrfToken) {
      headers["X-CSRF-Token"] = csrfToken;
    }
    return fetch(url, {
      method: "POST",
      credentials: "same-origin",
      headers: headers,
      body: JSON.stringify(body || {}),
    }).then(function(response) {
      return response.json().then(function(data) {
        if (!response.ok) {
          var message = data && data.error ? data.error : "Request failed";
          throw new Error(message);
        }
        return data;
      }).catch(function(err) {
        if (response.ok) {
          return {};
        }
        throw err;
      });
    });
  }

  function showError(button, message) {
    var container = button.closest("main") || document;
    var errorBox = container.querySelector("[data-webauthn-error]");
    var errorMessage = container.querySelector("[data-webauthn-error-message]");
    if (!errorBox || !errorMessage) {
      return;
    }
    errorMessage.textContent = message;
    errorBox.classList.remove("hidden");
  }

  function clearError(button) {
    var container = button.closest("main") || document;
    var errorBox = container.querySelector("[data-webauthn-error]");
    if (!errorBox) {
      return;
    }
    errorBox.classList.add("hidden");
  }

  function isWebAuthnSupported() {
    return !!(window.PublicKeyCredential && navigator.credentials);
  }

  function setSupportMessage() {
    var support = document.querySelector("[data-webauthn-support]");
    if (!support) {
      return;
    }
    if (isWebAuthnSupported()) {
      support.textContent = "";
      return;
    }
    support.textContent = "Passkeys are not supported in this browser.";
  }

  function withLoading(button, fn) {
    var originalText = button.textContent;
    button.disabled = true;
    button.textContent = button.getAttribute("data-loading-text") || "Working...";
    return fn().finally(function() {
      button.disabled = false;
      button.textContent = originalText;
    });
  }

  function handleLogin(button) {
    button.addEventListener("click", function() {
      clearError(button);
      if (!isWebAuthnSupported()) {
        showError(button, "Passkeys are not supported in this browser.");
        return;
      }
      withLoading(button, function() {
        var startUrl = button.getAttribute("data-start-url");
        var finishUrl = button.getAttribute("data-finish-url");
        var redirectUrl = button.getAttribute("data-redirect-url");
        var nextPath = button.getAttribute("data-next-path") || "";
        var rememberInput = button.getAttribute("data-remember-input");
        var finishWithNext = finishUrl;
        var queryParams = [];
        if (nextPath) {
          queryParams.push("next=" + encodeURIComponent(nextPath));
        }
        if (rememberInput) {
          var rememberField = document.querySelector(rememberInput);
          if (rememberField) {
            queryParams.push("remember=" + (rememberField.checked ? "1" : "0"));
          }
        }
        if (queryParams.length > 0) {
          finishWithNext += (finishWithNext.indexOf("?") === -1 ? "?" : "&") + queryParams.join("&");
        }
        return postJSON(startUrl, {}).then(function(options) {
          decodeRequestOptions(options);
          var publicKey = options.publicKey || options;
          var mediation = options.mediation;
          return navigator.credentials.get({
            publicKey: publicKey,
            mediation: mediation,
          });
        }).then(function(credential) {
          return postJSON(finishWithNext, serializeCredential(credential));
        }).then(function(result) {
          var destination = result.redirect || redirectUrl || "/";
          window.location.assign(destination);
        }).catch(function(err) {
          if (err && (err.name === "NotAllowedError" || err.name === "AbortError")) {
            return;
          }
          showError(button, err.message || "Login failed");
        });
      });
    });
  }

  function handleRegistration(button) {
    button.addEventListener("click", function() {
      clearError(button);
      if (!isWebAuthnSupported()) {
        showError(button, "Passkeys are not supported in this browser.");
        return;
      }
      withLoading(button, function() {
        var startUrl = button.getAttribute("data-start-url");
        var finishUrl = button.getAttribute("data-finish-url");
        var redirectUrl = button.getAttribute("data-redirect-url");
        var displayInput = button.getAttribute("data-display-name-input");
        var labelInput = button.getAttribute("data-passkey-label-input");
        var payload = {};
        if (displayInput) {
          var displayField = document.querySelector(displayInput);
          payload.displayName = displayField ? displayField.value : "";
        }
        if (labelInput) {
          var labelField = document.querySelector(labelInput);
          payload.label = labelField ? labelField.value : "";
        }
        return postJSON(startUrl, payload).then(function(options) {
          decodeCreationOptions(options);
          var publicKey = options.publicKey || options;
          var mediation = options.mediation;
          return navigator.credentials.create({
            publicKey: publicKey,
            mediation: mediation,
          });
        }).then(function(credential) {
          return postJSON(finishUrl, serializeCredential(credential));
        }).then(function(result) {
          if (redirectUrl) {
            window.location.assign(redirectUrl);
            return;
          }
          if (result && result.redirect) {
            window.location.assign(result.redirect);
            return;
          }
          window.location.reload();
        }).catch(function(err) {
          if (err && (err.name === "NotAllowedError" || err.name === "AbortError")) {
            return;
          }
          showError(button, err.message || "Registration failed");
        });
      });
    });
  }

  function init() {
    setSupportMessage();
    document.querySelectorAll("[data-webauthn-login]").forEach(handleLogin);
    document.querySelectorAll("[data-webauthn-register]").forEach(handleRegistration);
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", init);
  } else {
    init();
  }
})();
