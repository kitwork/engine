const body = { name: "a", role: "admin", pick: { name: "a", role: "admin" }, has: true, len: 9 };
const reply = { ok: false, error: "broken", length: 5, isError: "I" };
const plain = { a: 1, b: 2 };
const kept = body.pick("name");
let result = [body.pick.role, kept.has("role"), kept.name, body.has("token"), body.has("role"), body.len(), reply.error, reply.length, reply.isError, plain.error == null, plain.length, plain.isError].join(",");
