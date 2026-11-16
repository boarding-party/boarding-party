// personaLabel is a persona's name with its device, so the Start run list
// shows who browses on a phone. fields are the director's profile fields
// (GET /api/profile-fields); a device they do not name shows nothing.
export function personaLabel(persona, fields) {
  const device = persona.profile && persona.profile.device;
  const f = fields.find((x) => x.key === 'device');
  const o = device && f ? f.options.find((x) => x.value === device) : null;
  return o ? `${persona.name} (${o.label})` : persona.name;
}
