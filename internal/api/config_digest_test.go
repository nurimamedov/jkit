package api

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfigurationDigestSerializationEquivalence(t *testing.T) {
	source := []byte("<?xml version='1.1' encoding='UTF-8'?>\n<project b='2' a='1'>\n <description>&apos;hello&apos; &amp; текст</description>\n <script>  x\n y  </script>\n <empty/>\n</project>\n")
	destination := []byte(`<?xml version="1.1" encoding="UTF-8"?><project a="1" b="2"><description>'hello' &amp; текст</description><script><![CDATA[  x
 y  ]]></script><empty></empty></project>`)
	first, err := ConfigurationSHA256(source)
	require.NoError(t, err)
	second, err := ConfigurationSHA256(destination)
	require.NoError(t, err)
	assert.Equal(t, first, second)
	assert.Len(t, first, 64)
}

func TestConfigurationDigestPreservesConfiguration(t *testing.T) {
	pairs := [][2]string{
		{`<project><script> x </script></project>`, `<project><script>x</script></project>`},
		{`<project><value> </value></project>`, `<project><value/></project>`},
		{`<project><value><![CDATA[ ]]></value></project>`, `<project><value/></project>`},
		{`<project><value> a <b/> c </value></project>`, `<project><value>a<b/>c</value></project>`},
		{`<project><value>  <b/>a<b/>  </value></project>`, `<project><value><b/>a<b/></value></project>`},
		{`<project><value x="1"/></project>`, `<project><value x="2"/></project>`},
		{`<project xmlns="urn:a"/>`, `<project xmlns="urn:b"/>`},
		{`<project><!--approved--><value/></project>`, `<project><value/></project>`},
	}
	for _, pair := range pairs {
		first, err := ConfigurationSHA256([]byte(pair[0]))
		require.NoError(t, err)
		second, err := ConfigurationSHA256([]byte(pair[1]))
		require.NoError(t, err)
		assert.NotEqual(t, first, second, pair)
	}
	assert.Error(t, func() error { _, err := ConfigurationSHA256([]byte(`<!DOCTYPE project><project/>`)); return err }())
}
