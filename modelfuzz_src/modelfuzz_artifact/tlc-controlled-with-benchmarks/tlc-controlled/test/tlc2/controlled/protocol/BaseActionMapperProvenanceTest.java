package tlc2.controlled.protocol;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertFalse;
import static org.junit.Assert.assertTrue;

import java.util.Collections;
import java.util.List;

import org.junit.Test;

import tlc2.tool.Action;

public class BaseActionMapperProvenanceTest {
    @Test
    public void preservesIgnoredInputIndexesWithoutChangingLegacyFiltering() {
        BaseActionMapper mapper = new BaseActionMapper(Collections.emptyList()) {
            @Override
            protected Action mapAction(AbstractAction action) {
                return "mapped".equals(action.name) ? Action.UNKNOWN : null;
            }
        };
        String input = "["
            + "{\"Name\":\"mapped\",\"Params\":{},\"Reset\":false},"
            + "{\"Name\":\"SendMessage\",\"Params\":{},\"Reset\":false},"
            + "{\"Name\":\"\",\"Params\":{},\"Reset\":true}]";

        List<MappedAction> provenance = mapper.mapListOfActionsWithProvenance(input);
        assertEquals(3, provenance.size());
        assertEquals(0, provenance.get(0).getInputIndex());
        assertEquals("mapped", provenance.get(0).getInputName());
        assertFalse(provenance.get(0).isIgnored());
        assertEquals(1, provenance.get(1).getInputIndex());
        assertEquals("SendMessage", provenance.get(1).getInputName());
        assertTrue(provenance.get(1).isIgnored());
        assertEquals(2, provenance.get(2).getInputIndex());
        assertTrue(provenance.get(2).getAction().isReset());

        List<ActionWrapper> legacy = mapper.mapListOfActions(input);
        assertEquals(2, legacy.size());
        assertTrue(legacy.get(1).isReset());
    }
}
