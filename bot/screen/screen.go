package screen

import (
	"errors"
	"fmt"
	"io"

	"github.com/imfusheng/go-mc/bot"
	"github.com/imfusheng/go-mc/chat"
	"github.com/imfusheng/go-mc/data/packetid"
	"github.com/imfusheng/go-mc/level/component"
	"github.com/imfusheng/go-mc/nbt"
	pk "github.com/imfusheng/go-mc/net/packet"
)

type Manager struct {
	c *bot.Client

	Screens   map[int]Container
	Inventory Inventory
	Cursor    Slot
	events    EventsListener
	// The last received State ID from server
	stateID int32
}

func NewManager(c *bot.Client, e EventsListener) *Manager {
	m := &Manager{
		c:       c,
		Screens: make(map[int]Container),
		events:  e,
	}
	m.Screens[0] = &m.Inventory
	c.Events.AddListener(
		bot.PacketHandler{Priority: 0, ID: packetid.ClientboundOpenScreen, F: m.onOpenScreen},
		bot.PacketHandler{Priority: 0, ID: packetid.ClientboundContainerSetContent, F: m.onSetContentPacket},
		bot.PacketHandler{Priority: 0, ID: packetid.ClientboundContainerClose, F: m.onCloseScreen},
		bot.PacketHandler{Priority: 0, ID: packetid.ClientboundContainerSetSlot, F: m.onSetSlot},
	)
	return m
}

type ChangedSlots map[int]*Slot

func (m *Manager) ContainerClick(id int, slot int16, button byte, mode int32, slots ChangedSlots, carried *Slot) error {
	return m.c.Conn.WritePacket(pk.Marshal(
		packetid.ServerboundContainerClick,
		pk.UnsignedByte(id),
		pk.VarInt(m.stateID),
		pk.Short(slot),
		pk.Byte(button),
		pk.VarInt(mode),
		slots,
		carried,
	))
}

func (c ChangedSlots) WriteTo(w io.Writer) (n int64, err error) {
	n, err = pk.VarInt(len(c)).WriteTo(w)
	if err != nil {
		return
	}
	for i, v := range c {
		n1, err := pk.Short(i).WriteTo(w)
		if err != nil {
			return n + n1, err
		}
		n2, err := v.WriteTo(w)
		if err != nil {
			return n + n1 + n2, err
		}
		n += n1 + n2
	}
	return
}

func (m *Manager) onOpenScreen(p pk.Packet) error {
	var (
		ContainerID pk.VarInt
		Type        pk.VarInt
		Title       chat.Message
	)
	if err := p.Scan(&ContainerID, &Type, &Title); err != nil {
		return Error{err}
	}
	if _, ok := m.Screens[int(ContainerID)]; !ok {
		TypeInt32 := int32(Type)
		if TypeInt32 < 6 {
			Rows := TypeInt32 + 1
			chest := Chest{
				Type:  TypeInt32,
				Slots: make([]Slot, 9*Rows),
				Rows:  int(Rows),
				Title: Title,
			}
			m.Screens[int(ContainerID)] = &chest
		}
	} else {
		return errors.New("container id already exists in screens")
	}
	if m.events.Open != nil {
		if err := m.events.Open(int(ContainerID), int32(Type), Title); err != nil {
			return Error{err}
		}
	}
	return nil
}

func (m *Manager) onSetContentPacket(p pk.Packet) error {
	var (
		ContainerID pk.UnsignedByte
		StateID     pk.VarInt
		SlotData    []Slot
		CarriedItem Slot
	)
	if err := p.Scan(
		&ContainerID,
		&StateID,
		pk.Array(&SlotData),
		&CarriedItem,
	); err != nil {
		return Error{err}
	}
	m.stateID = int32(StateID)
	// copy the slot data to container
	container, ok := m.Screens[int(ContainerID)]
	if !ok {
		return Error{errors.New("setting content of non-exist container")}
	}
	for i, v := range SlotData {
		err := container.onSetSlot(i, v)
		if err != nil {
			return Error{err}
		}
		if m.events.SetSlot != nil {
			if err := m.events.SetSlot(int(ContainerID), i); err != nil {
				return Error{err}
			}
		}
	}
	return nil
}

func (m *Manager) onCloseScreen(p pk.Packet) error {
	var ContainerID pk.UnsignedByte
	if err := p.Scan(&ContainerID); err != nil {
		return Error{err}
	}
	if c, ok := m.Screens[int(ContainerID)]; ok {
		delete(m.Screens, int(ContainerID))
		if err := c.onClose(); err != nil {
			return Error{err}
		}
		if m.events.Close != nil {
			if err := m.events.Close(int(ContainerID)); err != nil {
				return Error{err}
			}
		}
	}
	return nil
}

func (m *Manager) onSetSlot(p pk.Packet) (err error) {
	var (
		ContainerID pk.Byte
		StateID     pk.VarInt
		SlotID      pk.Short
		SlotData    Slot
	)
	if err := p.Scan(&ContainerID, &StateID, &SlotID, &SlotData); err != nil {
		return Error{err}
	}

	m.stateID = int32(StateID)
	if ContainerID == -1 && SlotID == -1 {
		m.Cursor = SlotData
	} else if ContainerID == -2 {
		err = m.Inventory.onSetSlot(int(SlotID), SlotData)
	} else if c, ok := m.Screens[int(ContainerID)]; ok {
		err = c.onSetSlot(int(SlotID), SlotData)
	}

	if m.events.SetSlot != nil {
		if err := m.events.SetSlot(int(ContainerID), int(SlotID)); err != nil {
			return Error{err}
		}
	}
	if err != nil {
		return Error{err}
	}
	return nil
}

type Slot struct {
	ID                pk.VarInt
	Count             pk.VarInt
	Components        []component.DataComponent
	RemovedComponents []pk.VarInt

	// NBT is retained for source compatibility with the pre-1.20.5 Slot
	// representation. Protocol 767 does not encode this field; callers must
	// represent item metadata with Components instead.
	//
	// Deprecated: use Components and RemovedComponents.
	NBT nbt.RawMessage
}

func (s *Slot) WriteTo(w io.Writer) (n int64, err error) {
	if s == nil {
		return pk.VarInt(0).WriteTo(w)
	}
	if s.Count < 0 {
		return 0, fmt.Errorf("slot count must not be negative: %d", s.Count)
	}
	if s.Count > 0 && s.ID < 0 {
		return 0, fmt.Errorf("slot item ID must not be negative: %d", s.ID)
	}
	if s.NBT.Type != 0 || len(s.NBT.Data) != 0 {
		return 0, errors.New("slot NBT cannot be encoded in protocol 767; use data components")
	}
	if s.Count == 0 {
		if s.ID != 0 || len(s.Components) != 0 || len(s.RemovedComponents) != 0 {
			return 0, errors.New("empty slot must not contain an item ID or data components")
		}
		return s.Count.WriteTo(w)
	}

	componentIDs, err := validateSlotComponents(s.Components, s.RemovedComponents)
	if err != nil {
		return 0, err
	}

	fields := pk.Tuple{
		s.Count,
		s.ID,
		pk.VarInt(len(s.Components)),
		pk.VarInt(len(s.RemovedComponents)),
	}
	for i, value := range s.Components {
		fields = append(fields, componentIDs[i], componentWriter{
			id:    int32(componentIDs[i]),
			value: value,
		})
	}
	for _, id := range s.RemovedComponents {
		fields = append(fields, id)
	}
	return fields.WriteTo(w)
}

func (s *Slot) ReadFrom(r io.Reader) (n int64, err error) {
	if s == nil {
		return 0, errors.New("cannot decode a slot into a nil receiver")
	}

	var decoded Slot
	n1, err := decoded.Count.ReadFrom(r)
	n += n1
	if err != nil {
		return n, fmt.Errorf("read slot count: %w", err)
	}
	if decoded.Count < 0 {
		return n, fmt.Errorf("slot count must not be negative: %d", decoded.Count)
	}
	if decoded.Count == 0 {
		*s = decoded
		return n, nil
	}

	var addedCount, removedCount pk.VarInt
	for _, entry := range []struct {
		name  string
		field pk.FieldDecoder
	}{
		{name: "item ID", field: &decoded.ID},
		{name: "added component count", field: &addedCount},
		{name: "removed component count", field: &removedCount},
	} {
		n1, readErr := entry.field.ReadFrom(r)
		n += n1
		if readErr != nil {
			return n, fmt.Errorf("read slot %s: %w", entry.name, readErr)
		}
	}
	if err := validateComponentCount("added", addedCount); err != nil {
		return n, err
	}
	if err := validateComponentCount("removed", removedCount); err != nil {
		return n, err
	}
	if decoded.ID < 0 {
		return n, fmt.Errorf("slot item ID must not be negative: %d", decoded.ID)
	}

	decoded.Components = make([]component.DataComponent, 0, int(addedCount))
	decoded.RemovedComponents = make([]pk.VarInt, 0, int(removedCount))
	seen := make(map[int32]string, int(addedCount)+int(removedCount))
	for i := 0; i < int(addedCount); i++ {
		var wireID pk.VarInt
		n1, readErr := wireID.ReadFrom(r)
		n += n1
		if readErr != nil {
			return n, fmt.Errorf("read added component %d type: %w", i, readErr)
		}
		id := int32(wireID)
		name, ok := component.TypeName(id)
		if !ok {
			return n, &component.TypeError{Kind: component.ErrUnknownComponent, ID: id}
		}
		if previous, duplicate := seen[id]; duplicate {
			return n, fmt.Errorf("duplicate data component %q (type %d) in %s and added sets", name, id, previous)
		}
		value := component.NewComponent(id)
		if value == nil {
			return n, &component.TypeError{Kind: component.ErrUnsupportedComponent, ID: id, Name: name}
		}
		n1, readErr = readComponentPayload(r, id, value)
		n += n1
		if readErr != nil {
			return n, readErr
		}
		seen[id] = "added"
		decoded.Components = append(decoded.Components, value)
	}
	for i := 0; i < int(removedCount); i++ {
		var wireID pk.VarInt
		n1, readErr := wireID.ReadFrom(r)
		n += n1
		if readErr != nil {
			return n, fmt.Errorf("read removed component %d type: %w", i, readErr)
		}
		id := int32(wireID)
		name, ok := component.TypeName(id)
		if !ok {
			return n, &component.TypeError{Kind: component.ErrUnknownComponent, ID: id}
		}
		if previous, duplicate := seen[id]; duplicate {
			return n, fmt.Errorf("duplicate data component %q (type %d) in %s and removed sets", name, id, previous)
		}
		seen[id] = "removed"
		decoded.RemovedComponents = append(decoded.RemovedComponents, wireID)
	}

	*s = decoded
	return n, nil
}

// ErrComponentCodecPanic identifies a panic recovered at the component codec
// boundary.
var ErrComponentCodecPanic = errors.New("data component codec panicked")

// ComponentCodecPanicError reports an unsafe component codec implementation
// without allowing its panic to escape the Slot network boundary.
type ComponentCodecPanicError struct {
	Operation string
	TypeID    int32
	Name      string
	Panic     any
}

func (e *ComponentCodecPanicError) Error() string {
	return fmt.Sprintf("%s data component %q (type %d): %v: %v", e.Operation, e.Name, e.TypeID, ErrComponentCodecPanic, e.Panic)
}

func (e *ComponentCodecPanicError) Unwrap() error { return ErrComponentCodecPanic }

type componentWriter struct {
	id    int32
	value component.DataComponent
}

func (c componentWriter) WriteTo(w io.Writer) (n int64, err error) {
	name, _ := component.TypeName(c.id)
	defer func() {
		if recovered := recover(); recovered != nil {
			err = &ComponentCodecPanicError{
				Operation: "encode",
				TypeID:    c.id,
				Name:      name,
				Panic:     recovered,
			}
		}
	}()
	n, err = c.value.WriteTo(w)
	if err != nil {
		err = fmt.Errorf("encode data component %q (type %d): %w", name, c.id, err)
	}
	return n, err
}

func readComponentPayload(r io.Reader, id int32, value component.DataComponent) (n int64, err error) {
	name, _ := component.TypeName(id)
	defer func() {
		if recovered := recover(); recovered != nil {
			err = &ComponentCodecPanicError{
				Operation: "decode",
				TypeID:    id,
				Name:      name,
				Panic:     recovered,
			}
		}
	}()
	n, err = value.ReadFrom(r)
	if err != nil {
		err = fmt.Errorf("decode data component %q (type %d): %w", name, id, err)
	}
	return n, err
}

func validateComponentCount(kind string, count pk.VarInt) error {
	if count < 0 {
		return fmt.Errorf("slot %s component count must not be negative: %d", kind, count)
	}
	if int(count) > component.TypeCount() {
		return fmt.Errorf("slot %s component count %d exceeds protocol 767 registry size %d", kind, count, component.TypeCount())
	}
	return nil
}

func validateSlotComponents(added []component.DataComponent, removed []pk.VarInt) ([]pk.VarInt, error) {
	if err := validateComponentCount("added", pk.VarInt(len(added))); err != nil {
		return nil, err
	}
	if err := validateComponentCount("removed", pk.VarInt(len(removed))); err != nil {
		return nil, err
	}

	ids := make([]pk.VarInt, len(added))
	seen := make(map[int32]string, len(added)+len(removed))
	for i, value := range added {
		id, err := component.TypeID(value)
		if err != nil {
			return nil, fmt.Errorf("resolve added component %d: %w", i, err)
		}
		name, _ := component.TypeName(id)
		if component.NewComponent(id) == nil {
			return nil, &component.TypeError{Kind: component.ErrUnsupportedComponent, ID: id, Name: name}
		}
		if previous, duplicate := seen[id]; duplicate {
			return nil, fmt.Errorf("duplicate data component %q (type %d) in %s and added sets", name, id, previous)
		}
		seen[id] = "added"
		ids[i] = pk.VarInt(id)
	}
	for i, wireID := range removed {
		id := int32(wireID)
		name, ok := component.TypeName(id)
		if !ok {
			return nil, &component.TypeError{Kind: component.ErrUnknownComponent, ID: id}
		}
		if previous, duplicate := seen[id]; duplicate {
			return nil, fmt.Errorf("duplicate data component %q (type %d) in %s and removed sets", name, id, previous)
		}
		seen[id] = fmt.Sprintf("removed component %d", i)
	}
	return ids, nil
}

type Container interface {
	onSetSlot(i int, s Slot) error
	onClose() error
}

type Error struct {
	Err error
}

func (e Error) Error() string {
	return "bot/screen: " + e.Err.Error()
}

func (e Error) Unwrap() error {
	return e.Err
}
