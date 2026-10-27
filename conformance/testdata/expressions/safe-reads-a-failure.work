const findUser = (id) => {
  if (id == 0) { fail("không có người dùng " + id); }
  return { id: id, name: "an" };
};
const found = safe(() => findUser(1));
const missing = safe(() => findUser(0));
const { value: user, error: userError } = safe(() => findUser(2));
const nested = safe(() => {
  const inner = safe(() => findUser(0));
  return inner.error ? "dự phòng" : inner.value;
});
const typed = safe(() => "a" * 2);
let result = [found.value.name, found.error, missing.value, missing.error.code, missing.error.message, missing.error.at, user.id, userError, nested.value, nested.error, typed.error.code, typed.error.message].join("|");
