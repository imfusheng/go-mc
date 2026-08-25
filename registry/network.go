package registry

import (
	"errors"
	"fmt"
	"io"
	"strconv"

	pk "github.com/imfusheng/go-mc/net/packet"
)

// Network decoder limits are deliberately below the generic packet byte
// limit: these values count objects or retained pointers, not wire bytes.
// They are hard caps for untrusted Registry Data and Update Tags packets.
const (
	MaxNetworkRegistryEntries = 1 << 16
	MaxNetworkRegistryTags    = 1 << 15
	MaxNetworkTagEntries      = 1 << 16
	MaxNetworkTagReferences   = 1 << 18
)

func (reg *Registry[E]) ReadFrom(r io.Reader) (int64, error) {
	var length pk.VarInt
	n, err := length.ReadFrom(r)
	if err != nil {
		return n, err
	}
	if err := validateNetworkCount(length, "registry entry", MaxNetworkRegistryEntries); err != nil {
		return n, err
	}

	reg.Clear()

	var key pk.Identifier
	var hasData pk.Boolean
	for i := 0; i < int(length); i++ {
		var data E
		var n1, n2, n3 int64

		n1, err = key.ReadFrom(r)
		if err != nil {
			return n + n1, err
		}

		n2, err = hasData.ReadFrom(r)
		if err != nil {
			return n + n1 + n2, err
		}

		if hasData {
			n3, err = pk.NBTField{V: &data, AllowUnknownFields: true}.ReadFrom(r)
			if err != nil {
				return n + n1 + n2 + n3, err
			}
		}
		reg.put(string(key), data, bool(hasData))

		n += n1 + n2 + n3
	}
	return n, nil
}

// WriteTo encodes a configuration Registry Data entries array. Presence bits
// are preserved when a registry was decoded from the network; values inserted
// with Put are encoded with data.
func (reg Registry[E]) WriteTo(w io.Writer) (int64, error) {
	if len(reg.names) != len(reg.values) || len(reg.present) != len(reg.values) {
		return 0, errors.New("registry: inconsistent internal lengths")
	}
	if len(reg.values) > MaxNetworkRegistryEntries {
		return 0, fmt.Errorf("registry entry count %d exceeds maximum %d", len(reg.values), MaxNetworkRegistryEntries)
	}
	n, err := pk.VarInt(len(reg.values)).WriteTo(w)
	if err != nil {
		return n, err
	}
	for i := range reg.values {
		n1, err := pk.Identifier(reg.names[i]).WriteTo(w)
		n += n1
		if err != nil {
			return n, err
		}
		n1, err = pk.Boolean(reg.present[i]).WriteTo(w)
		n += n1
		if err != nil {
			return n, err
		}
		if reg.present[i] {
			n1, err = (pk.NBTField{V: &reg.values[i]}).WriteTo(w)
			n += n1
			if err != nil {
				return n, err
			}
		}
	}
	return n, nil
}

func (reg *Registry[E]) ReadTagsFrom(r io.Reader) (int64, error) {
	var count pk.VarInt
	n, err := count.ReadFrom(r)
	if err != nil {
		return n, err
	}
	if err := validateNetworkCount(count, "registry tag", MaxNetworkRegistryTags); err != nil {
		return n, err
	}
	if reg.tags == nil {
		reg.tags = make(map[string][]*E)
	}

	var tag pk.Identifier
	var length pk.VarInt
	var totalReferences int64
	for i := 0; i < int(count); i++ {
		var n1, n2, n3 int64

		n1, err = tag.ReadFrom(r)
		if err != nil {
			return n + n1, err
		}

		n2, err = length.ReadFrom(r)
		if err != nil {
			return n + n1 + n2, err
		}
		if err := validateNetworkCount(length, "registry tag entry", MaxNetworkTagEntries); err != nil {
			return n + n1 + n2, fmt.Errorf("tag %q: %w", tag, err)
		}
		if int64(length) > int64(MaxNetworkTagReferences)-totalReferences {
			return n + n1 + n2, fmt.Errorf("registry tag reference count exceeds maximum %d", MaxNetworkTagReferences)
		}
		totalReferences += int64(length)

		n += n1 + n2
		values := make([]*E, int(length))

		var id pk.VarInt
		for i := 0; i < int(length); i++ {
			n3, err = id.ReadFrom(r)
			if err != nil {
				return n + n3, err
			}

			if id < 0 || int(id) >= len(reg.values) {
				err = errors.New("invalid id: " + strconv.Itoa(int(id)))
				return n + n3, err
			}

			values[i] = &reg.values[id]
			n += n3
		}

		reg.tags[string(tag)] = values
	}
	return n, nil
}

func validateNetworkCount(count pk.VarInt, field string, maximum int) error {
	if count < 0 || int64(count) > int64(maximum) {
		return fmt.Errorf("invalid %s count %d (maximum %d)", field, count, maximum)
	}
	return nil
}
