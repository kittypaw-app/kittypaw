(() => {
  const form = document.querySelector("#chat-form");
  const input = document.querySelector("#chat-input");
  const log = document.querySelector("#chat-log");

  form?.addEventListener("submit", (event) => {
    event.preventDefault();
    const text = input.value.trim();
    if (!text) return;
    const item = document.createElement("p");
    item.textContent = text;
    log.appendChild(item);
    input.value = "";
  });
})();
